package servicebroker

import (
	"errors"

	authclient "github.com/openshift/origin/pkg/auth/client"
	"github.com/openshift/origin/pkg/client"
	"github.com/openshift/origin/pkg/cmd/server/bootstrappolicy"
	templateapi "github.com/openshift/origin/pkg/template/api"
	templateinformer "github.com/openshift/origin/pkg/template/generated/informers/internalversion/template/internalversion"
	templateclientset "github.com/openshift/origin/pkg/template/generated/internalclientset"
	internalversiontemplate "github.com/openshift/origin/pkg/template/generated/internalclientset/typed/template/internalversion"
	templatelister "github.com/openshift/origin/pkg/template/generated/listers/template/internalversion"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/authentication/user"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubernetes/pkg/apis/authorization"
	kclientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
)

type Broker struct {
	oc                 *client.Client
	kc                 kclientset.Interface
	templateclient     internalversiontemplate.TemplateInterface
	restconfig         restclient.Config
	lister             templatelister.TemplateLister
	templateNamespaces map[string]struct{}
}

func NewBroker(restconfig restclient.Config, informer templateinformer.TemplateInformer, namespaces []string) *Broker {
	informer.Informer().AddIndexers(cache.Indexers{
		templateapi.TemplateUIDIndex: func(obj interface{}) ([]string, error) {
			return []string{string(obj.(*templateapi.Template).UID)}, nil
		}})

	templateNamespaces := map[string]struct{}{}
	for _, namespace := range namespaces {
		templateNamespaces[namespace] = struct{}{}
	}

	restconfig = authclient.NewImpersonatingConfig(&user.DefaultInfo{Name: "system:serviceaccount:openshift-infra:" + bootstrappolicy.InfraTemplateServiceBrokerServiceAccountName}, restconfig)

	return &Broker{
		oc:                 client.NewOrDie(&restconfig),
		kc:                 kclientset.NewForConfigOrDie(&restconfig),
		templateclient:     templateclientset.NewForConfigOrDie(&restconfig).Template(),
		lister:             informer.Lister(),
		templateNamespaces: templateNamespaces,
	}
}

func (b *Broker) authorize(u user.Info, resourceAttributes *authorization.ResourceAttributes) error {
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

	resp, err := b.kc.Authorization().SubjectAccessReviews().Create(sar)
	if err == nil && resp != nil && resp.Status.Allowed {
		return nil
	}
	if err == nil {
		err = errors.New(resp.Status.Reason)
	}
	return kerrors.NewForbidden(schema.GroupResource{Group: resourceAttributes.Group, Resource: resourceAttributes.Resource}, resourceAttributes.Name, err)
}
