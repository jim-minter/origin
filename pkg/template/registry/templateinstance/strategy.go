package templateinstance

import (
	"errors"
	"fmt"

	templateapi "github.com/openshift/origin/pkg/template/api"
	"github.com/openshift/origin/pkg/template/api/validation"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/authentication/user"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/names"
	kapi "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/apis/authorization"
	kclientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
)

// templateInstanceStrategy implements behavior for Templates
type templateInstanceStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
	kc kclientset.Interface
}

func NewStrategy(kc kclientset.Interface) *templateInstanceStrategy {
	return &templateInstanceStrategy{kapi.Scheme, names.SimpleNameGenerator, kc}
}

// NamespaceScoped is true for templateinstances.
func (templateInstanceStrategy) NamespaceScoped() bool {
	return true
}

// PrepareForUpdate clears fields that are not allowed to be set by end users on update.
func (templateInstanceStrategy) PrepareForUpdate(ctx apirequest.Context, obj, old runtime.Object) {
}

// Canonicalize normalizes the object after validation.
func (templateInstanceStrategy) Canonicalize(obj runtime.Object) {
}

// PrepareForCreate clears fields that are not allowed to be set by end users on creation.
func (templateInstanceStrategy) PrepareForCreate(ctx apirequest.Context, obj runtime.Object) {
	templateInstance := obj.(*templateapi.TemplateInstance)

	if templateInstance.Spec.Requester == nil {
		user, _ := apirequest.UserFrom(ctx)
		templateInstance.Spec.Requester = &templateapi.TemplateInstanceRequester{
			Username: user.GetName(),
		}
	}
}

// Validate validates a new templateinstance.
func (s *templateInstanceStrategy) Validate(ctx apirequest.Context, obj runtime.Object) field.ErrorList {
	user, ok := apirequest.UserFrom(ctx)
	if !ok {
		return field.ErrorList{field.InternalError(field.NewPath(""), errors.New("user not found in context"))}
	}

	templateInstance := obj.(*templateapi.TemplateInstance)
	allErrs := validation.ValidateTemplateInstance(templateInstance)
	allErrs = append(allErrs, s.validateImpersonation(templateInstance, user)...)

	return allErrs
}

// AllowCreateOnUpdate is false for templateinstances.
func (templateInstanceStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (templateInstanceStrategy) AllowUnconditionalUpdate() bool {
	return false
}

// ValidateUpdate is the default update validation for an end user.
func (s *templateInstanceStrategy) ValidateUpdate(ctx apirequest.Context, obj, old runtime.Object) field.ErrorList {
	user, ok := apirequest.UserFrom(ctx)
	if !ok {
		return field.ErrorList{field.InternalError(field.NewPath(""), errors.New("user not found in context"))}
	}

	templateInstance := obj.(*templateapi.TemplateInstance)
	oldTemplateInstance := old.(*templateapi.TemplateInstance)
	allErrs := validation.ValidateTemplateInstanceUpdate(templateInstance, oldTemplateInstance)
	allErrs = append(allErrs, s.validateImpersonation(templateInstance, user)...)

	return allErrs
}

// Matcher returns a generic matcher for a given label and field selector.
func Matcher(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// GetAttrs returns labels and fields of a given object for filtering purposes
func GetAttrs(o runtime.Object) (labels.Set, fields.Set, error) {
	obj, ok := o.(*templateapi.TemplateInstance)
	if !ok {
		return nil, nil, fmt.Errorf("not a TemplateInstance")
	}
	return labels.Set(obj.Labels), SelectableFields(obj), nil
}

// SelectableFields returns a field set that can be used for filter selection
func SelectableFields(obj *templateapi.TemplateInstance) fields.Set {
	return templateapi.TemplateInstanceToSelectableFields(obj)
}

func (s *templateInstanceStrategy) authorize(u user.Info, resourceAttributes *authorization.ResourceAttributes) error {
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

	resp, err := s.kc.Authorization().SubjectAccessReviews().Create(sar)
	if err == nil && resp != nil && resp.Status.Allowed {
		return nil
	}
	if err == nil {
		err = errors.New(resp.Status.Reason)
	}
	return kerrors.NewForbidden(schema.GroupResource{Group: resourceAttributes.Group, Resource: resourceAttributes.Resource}, resourceAttributes.Name, err)
}

func (s *templateInstanceStrategy) validateImpersonation(templateInstance *templateapi.TemplateInstance, userinfo user.Info) field.ErrorList {
	if templateInstance.Spec.Requester == nil || templateInstance.Spec.Requester.Username == "" {
		return field.ErrorList{field.Required(field.NewPath("spec.requester.username"), "")}
	}

	if templateInstance.Spec.Requester.Username != userinfo.GetName() {
		if err := s.authorize(userinfo, &authorization.ResourceAttributes{
			Namespace: templateInstance.Namespace,
			Verb:      "assign",
			Group:     templateapi.GroupName,
			Resource:  "templateinstances",
		}); err != nil {
			return field.ErrorList{field.Forbidden(field.NewPath("spec.impersonateUser"), "impersonation forbidden")}
		}
	}

	return nil
}
