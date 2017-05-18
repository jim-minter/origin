package controller

import (
	"errors"
	"fmt"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/tools/cache"
	kapi "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/apis/authorization"
	"k8s.io/kubernetes/pkg/kubectl/resource"

	"github.com/openshift/origin/pkg/api/latest"
	"github.com/openshift/origin/pkg/client"
	"github.com/openshift/origin/pkg/config/cmd"
	templateapi "github.com/openshift/origin/pkg/template/api"
	templateapiv1 "github.com/openshift/origin/pkg/template/api/v1"
	internalversiontemplate "github.com/openshift/origin/pkg/template/generated/internalclientset/typed/template/internalversion"
	kclientsetinternal "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
)

type TemplateInstanceController struct {
	oc             *client.Client
	kc             kclientsetinternal.Interface
	templateclient internalversiontemplate.TemplateInterface
}

func NewTemplateInstanceController(oc *client.Client, kc kclientsetinternal.Interface, templateclient internalversiontemplate.TemplateInterface, informer cache.SharedIndexInformer) cache.Controller {
	c := TemplateInstanceController{
		oc:             oc,
		kc:             kc,
		templateclient: templateclient,
	}

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			c.handle(obj.(*templateapi.TemplateInstance))
		},
		UpdateFunc: func(_, obj interface{}) {
			c.handle(obj.(*templateapi.TemplateInstance))
		},
		DeleteFunc: func(obj interface{}) {
		},
	})

	return informer.GetController()
}

func (c *TemplateInstanceController) handle(templateInstance *templateapi.TemplateInstance) error {
	for _, condition := range templateInstance.Status.Conditions {
		if condition.Type == templateapi.TemplateInstanceReady && condition.Status == kapi.ConditionTrue ||
			condition.Type == templateapi.TemplateInstanceInstantiateFailure && condition.Status == kapi.ConditionTrue {
			return nil
		}
	}

	err := c.provision(templateInstance)
	if err == nil {
		templateInstance.Status.Conditions = []templateapi.TemplateInstanceCondition{
			{
				Type:               templateapi.TemplateInstanceReady,
				Status:             kapi.ConditionTrue,
				LastTransitionTime: metav1.Now(),
				Reason:             "Created",
			},
		}

	} else {
		templateInstance.Status.Conditions = []templateapi.TemplateInstanceCondition{
			{
				Type:               templateapi.TemplateInstanceInstantiateFailure,
				Status:             kapi.ConditionTrue,
				LastTransitionTime: metav1.Now(),
				Reason:             "Failed",
				Message:            err.Error(),
			},
		}
	}

	_, err = c.templateclient.TemplateInstances(templateInstance.Namespace).Update(templateInstance)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("TemplateInstance update failed: %v", err))
	}
	return err
}

func (c *TemplateInstanceController) authorize(u user.Info, resourceAttributes *authorization.ResourceAttributes) error {
	sar := &authorization.SubjectAccessReview{
		Spec: authorization.SubjectAccessReviewSpec{
			ResourceAttributes: resourceAttributes,
			User:               u.GetName(),
			Groups:             u.GetGroups(),
		},
	}

	if extra := u.GetExtra(); len(extra) > 0 {
		sar.Spec.Extra = map[string]authorization.ExtraValue{}
		for k, v := range extra {
			sar.Spec.Extra[k] = authorization.ExtraValue(v)
		}
	}

	resp, err := c.kc.Authorization().SubjectAccessReviews().Create(sar)
	if err == nil && resp != nil && resp.Status.Allowed {
		return nil
	}
	if err == nil {
		err = errors.New(resp.Status.Reason)
	}
	return kerrors.NewForbidden(schema.GroupResource{Group: resourceAttributes.Group, Resource: resourceAttributes.Resource}, resourceAttributes.Name, err)
}

func (c *TemplateInstanceController) provision(templateInstance *templateapi.TemplateInstance) error {
	if templateInstance.Spec.Requester == nil || templateInstance.Spec.Requester.Username == "" {
		return fmt.Errorf("spec.requester.username not set")
	}

	u := &user.DefaultInfo{Name: templateInstance.Spec.Requester.Username}

	if err := c.authorize(u, &authorization.ResourceAttributes{
		Namespace: templateInstance.Namespace,
		Verb:      "get",
		Group:     kapi.GroupName,
		Resource:  "secrets",
	}); err != nil {
		return err
	}

	secret, err := c.kc.Core().Secrets(templateInstance.Namespace).Get(templateInstance.Spec.Secret.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	templateCopy, err := kapi.Scheme.DeepCopy(&templateInstance.Spec.Template)
	if err != nil {
		return err
	}
	template := templateCopy.(*templateapi.Template)

	if template.ObjectLabels == nil {
		template.ObjectLabels = make(map[string]string)
	}
	template.ObjectLabels[templateapi.TemplateInstanceLabel] = templateInstance.Name

	for i, param := range template.Parameters {
		if value, ok := secret.Data[param.Name]; ok {
			template.Parameters[i].Value = string(value)
			template.Parameters[i].Generate = ""
		}
	}

	if err = c.authorize(u, &authorization.ResourceAttributes{
		Namespace: templateInstance.Namespace,
		Verb:      "create",
		Group:     templateapi.GroupName,
		Resource:  "templateconfigs",
	}); err != nil {
		return err
	}

	template, err = c.oc.TemplateConfigs(templateInstance.Namespace).Create(template)
	if err != nil {
		return err
	}

	errs := runtime.DecodeList(template.Objects, kapi.Codecs.UniversalDecoder())
	if len(errs) > 0 {
		return errs[0]
	}

	for _, obj := range template.Objects {
		meta, _ := meta.Accessor(obj)
		ref := meta.GetOwnerReferences()
		ref = append(ref, metav1.OwnerReference{
			APIVersion: templateapiv1.SchemeGroupVersion.String(),
			Kind:       "TemplateInstance",
			Name:       templateInstance.Name,
			UID:        templateInstance.UID,
		})
		meta.SetOwnerReferences(ref)
	}

	bulk := cmd.Bulk{
		Mapper: &resource.Mapper{
			RESTMapper:  client.DefaultMultiRESTMapper(),
			ObjectTyper: kapi.Scheme,
			ClientMapper: resource.ClientMapperFunc(func(mapping *meta.RESTMapping) (resource.RESTClient, error) {
				if latest.OriginKind(mapping.GroupVersionKind) {
					return c.oc, nil
				}
				return c.kc.Core().RESTClient(), nil
			}),
		},
		Op: func(info *resource.Info, namespace string, obj runtime.Object) (runtime.Object, error) {
			if len(info.Namespace) > 0 {
				namespace = info.Namespace
			}
			if err = c.authorize(u, &authorization.ResourceAttributes{
				Namespace: namespace,
				Verb:      "create",
				Group:     info.Mapping.GroupVersionKind.Group,
				Resource:  info.Mapping.Resource,
			}); err != nil {
				return nil, err
			}
			return obj, nil
		},
	}
	errs = bulk.Run(&kapi.List{Items: template.Objects}, templateInstance.Namespace)
	if len(errs) > 0 {
		return utilerrors.NewAggregate(errs)
	}

	bulk.Op = cmd.Create
	errs = bulk.Run(&kapi.List{Items: template.Objects}, templateInstance.Namespace)
	if len(errs) > 0 {
		return utilerrors.NewAggregate(errs)
	}

	return nil
}
