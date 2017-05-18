package etcd

import (
	"github.com/openshift/origin/pkg/template/api"
	rest "github.com/openshift/origin/pkg/template/registry/templateinstance"
	"github.com/openshift/origin/pkg/util/restoptions"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/generic/registry"
	kapi "k8s.io/kubernetes/pkg/api"
	kclientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
)

// REST implements a RESTStorage for templateinstances against etcd
type REST struct {
	*registry.Store
}

// NewREST returns a RESTStorage object that will work against templateinstances.
func NewREST(optsGetter restoptions.Getter, kc kclientset.Interface) (*REST, error) {
	strategy := rest.NewStrategy(kc)

	store := &registry.Store{
		Copier:            kapi.Scheme,
		NewFunc:           func() runtime.Object { return &api.TemplateInstance{} },
		NewListFunc:       func() runtime.Object { return &api.TemplateInstanceList{} },
		PredicateFunc:     rest.Matcher,
		QualifiedResource: api.Resource("templateinstances"),

		CreateStrategy: strategy,
		UpdateStrategy: strategy,
	}

	options := &generic.StoreOptions{RESTOptions: optsGetter, AttrFunc: rest.GetAttrs}
	if err := store.CompleteWithOptions(options); err != nil {
		return nil, err
	}

	return &REST{store}, nil
}
