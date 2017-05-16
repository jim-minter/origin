package servicebroker

import (
	"errors"

	authclient "github.com/openshift/origin/pkg/auth/client"
	authorizationapi "github.com/openshift/origin/pkg/authorization/api"
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

func (b *Broker) authorize(u user.Info, action *authorizationapi.Action) error {
	sar := authorizationapi.AddUserToSAR(u, &authorizationapi.SubjectAccessReview{Action: *action})
	resp, err := b.oc.SubjectAccessReviews().Create(sar)
	if err == nil && resp != nil && resp.Allowed {
		return nil
	}
	if err == nil {
		err = errors.New(resp.Reason)
	}
	return kerrors.NewForbidden(schema.GroupResource{Group: action.Group, Resource: action.Resource}, action.ResourceName, err)
}
