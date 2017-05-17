package projects

import (
	"fmt"
	"io"

	"github.com/golang/glog"
	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	kcmdutil "k8s.io/kubernetes/pkg/kubectl/cmd/util"
	"k8s.io/kubernetes/pkg/kubectl/resource"

	"github.com/openshift/origin/pkg/cmd/admin/migrate"
	"github.com/openshift/origin/pkg/cmd/templates"
	"github.com/openshift/origin/pkg/cmd/util/clientcmd"
)

var (
	internalMigrateProjectLong = templates.LongDesc(`
		Migrate internal object storage via update

		This command invokes an update operation on every API object reachable by the caller. This forces
		the server to write to the underlying storage if the object representation has changed. Use this
		command to ensure that the most recent storage changes have been applied to all objects (storage
		version, storage encoding, any newer object defaults).

		To operate on a subset of resources, use the --include flag. If you encounter errors during a run
		the command will output a list of resources that received errors, which you can then re-run the
		command on. You may also specify --from-key and --to-key to restrict the set of resource names
		to operate on (key is NAMESPACE/NAME for resources in namespaces or NAME for cluster scoped
		resources). --from-key is inclusive if specified, while --to-key is exclusive.

		By default, events are not migrated since they expire within a very short period of time. If you
		have significantly increased the expiration time of events, run a migration with --include=events

		WARNING: This is a slow command and will put significant load on an API server. It may also
		result in significant intra-cluster traffic.`)

	internalMigrateProjectExample = templates.Examples(`
		# Perform a dry-run of updating all objects
	  %[1]s

	  # To actually perform the update, the confirm flag must be appended
	  %[1]s --confirm`)
)

type MigrateProjectOptions struct {
	migrate.ResourceOptions
}

// NewCmdMigrateProject implements a MigrateProject command
func NewCmdMigrateProject(name, fullName string, f *clientcmd.Factory, in io.Reader, out, errout io.Writer) *cobra.Command {
	options := &MigrateProjectOptions{
		ResourceOptions: migrate.ResourceOptions{
			In:      in,
			Out:     out,
			ErrOut:  errout,
			Include: []string{"projects"},
		},
	}
	cmd := &cobra.Command{
		Use:     name,
		Short:   "Update the stored version of API objects",
		Long:    internalMigrateProjectLong,
		Example: fmt.Sprintf(internalMigrateProjectExample, fullName),
		Run: func(cmd *cobra.Command, args []string) {
			kcmdutil.CheckErr(options.Complete(f, cmd, args))
			kcmdutil.CheckErr(options.Validate())
			kcmdutil.CheckErr(options.Run())
		},
	}
	options.ResourceOptions.Bind(cmd)

	return cmd
}

func (o *MigrateProjectOptions) Complete(f *clientcmd.Factory, c *cobra.Command, args []string) error {
	o.ResourceOptions.SaveFn = o.save
	return o.ResourceOptions.Complete(f, c)
}

func (o MigrateProjectOptions) Validate() error {
	return o.ResourceOptions.Validate()
}

func (o MigrateProjectOptions) Run() error {
	return o.ResourceOptions.Visitor().Visit(func(info *resource.Info) (migrate.Reporter, error) {
		return o.transform(info.Object)
	})
}

// save invokes the API to alter an object. The reporter passed to this method is the same returned by
// the migration visitor method (for this type, transformProject). It should return an error
// if the input type cannot be saved. It returns migrate.ErrRecalculate if migration should be re-run
// on the provided object.
func (o *MigrateProjectOptions) save(info *resource.Info, reporter migrate.Reporter) error {
	switch info.Object.(type) {
	// TODO: add any custom mutations necessary
	default:
		// load the body and save it back, without transformation to avoid losing fields
		get := info.Client.Get().
			Resource(info.Mapping.Resource).
			NamespaceIfScoped(info.Namespace, info.Mapping.Scope.Name() == meta.RESTScopeNameNamespace).
			Name(info.Name).Do()
		data, err := get.Raw()
		if err != nil {
			return migrate.DefaultRetriable(info, err)
		}
		update := info.Client.Put().
			Resource(info.Mapping.Resource).
			NamespaceIfScoped(info.Namespace, info.Mapping.Scope.Name() == meta.RESTScopeNameNamespace).
			Name(info.Name).Body(data).
			Do()
		if err := update.Error(); err != nil {
			return migrate.DefaultRetriable(info, err)
		}

		if oldObject, err := get.Get(); err == nil {
			info.Refresh(oldObject, true)
			oldVersion := info.ResourceVersion
			if object, err := update.Get(); err == nil {
				info.Refresh(object, true)
				if info.ResourceVersion == oldVersion {
					return migrate.ErrUnchanged
				}
			} else {
				glog.V(4).Infof("unable to calculate resource version: %v", err)
			}
		} else {
			glog.V(4).Infof("unable to calculate resource version: %v", err)
		}
	}
	return nil
}

func (o *MigrateProjectOptions) transform(obj runtime.Object) (migrate.Reporter, error) {
	return reporter(true), nil
}

// reporter implements the Reporter interface for a boolean.
type reporter bool

func (r reporter) Changed() bool {
	return bool(r)
}
