package list

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/openshift/odo/pkg/catalog"
	"github.com/openshift/odo/pkg/log"
	"github.com/openshift/odo/pkg/machineoutput"
	catalogutil "github.com/openshift/odo/pkg/odo/cli/catalog/util"
	"github.com/openshift/odo/pkg/odo/genericclioptions"
	"github.com/openshift/odo/pkg/odo/util/experimental"
	"github.com/openshift/odo/pkg/odo/util/pushtarget"
	"github.com/openshift/odo/pkg/util"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"
)

const componentsRecommendedCommandName = "components"

var componentsExample = `  # Get the supported components
  %[1]s`

// ListComponentsOptions encapsulates the options for the odo catalog list components command
type ListComponentsOptions struct {
	// display both supported and unsupported devfile components
	listAllDevfileComponents bool
	// list of known images
	catalogList catalog.ComponentTypeList
	// list of known devfiles
	catalogDevfileList catalog.DevfileComponentTypeList
	// generic context options common to all commands
	*genericclioptions.Context
}

// NewListComponentsOptions creates a new ListComponentsOptions instance
func NewListComponentsOptions() *ListComponentsOptions {
	return &ListComponentsOptions{}
}

// Complete completes ListComponentsOptions after they've been created
func (o *ListComponentsOptions) Complete(name string, cmd *cobra.Command, args []string) (err error) {

	tasks := util.NewConcurrentTasks(2)

	if !pushtarget.IsPushTargetDocker() {
		o.Context = genericclioptions.NewContext(cmd)

		tasks.Add(util.ConcurrentTask{ToRun: func(errChannel chan error) {
			o.catalogList, err = catalog.ListComponents(o.Client)
			if err != nil {
				if experimental.IsExperimentalModeEnabled() {
					klog.V(4).Info("Please log in to an OpenShift cluster to list OpenShift/s2i components")
				} else {
					errChannel <- err
				}
			} else {
				o.catalogList.Items = catalogutil.FilterHiddenComponents(o.catalogList.Items)
			}
		}})
	}

	if experimental.IsExperimentalModeEnabled() {
		tasks.Add(util.ConcurrentTask{ToRun: func(errChannel chan error) {
			o.catalogDevfileList, err = catalog.ListDevfileComponents("")
			if o.catalogDevfileList.DevfileRegistries == nil {
				log.Warning("Please run 'odo registry add <registry name> <registry URL>' to add registry for listing devfile components\n")
			}
			if err != nil {
				errChannel <- err
			}
		}})
	}

	return tasks.Run()
}

// Validate validates the ListComponentsOptions based on completed values
func (o *ListComponentsOptions) Validate() (err error) {
	if len(o.catalogList.Items) == 0 && len(o.catalogDevfileList.Items) == 0 {
		return fmt.Errorf("no deployable components found")
	}

	return err
}

type combinedCatalogList struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	S2iItems          []catalog.ComponentType        `json:"s2iItems,omitempty"`
	DevfileItems      []catalog.DevfileComponentType `json:"devfileItems,omitempty"`
}

// Run contains the logic for the command associated with ListComponentsOptions
func (o *ListComponentsOptions) Run() (err error) {
	if log.IsJSON() {
		for i, image := range o.catalogList.Items {
			// here we don't care about the unsupported tags (second return value)
			supported, _ := catalog.SliceSupportedTags(image)
			o.catalogList.Items[i].Spec.SupportedTags = supported
		}
		combinedList := combinedCatalogList{
			TypeMeta: metav1.TypeMeta{
				Kind:       "List",
				APIVersion: "odo.dev/v1alpha1",
			},
			S2iItems:     o.catalogList.Items,
			DevfileItems: o.catalogDevfileList.Items,
		}
		machineoutput.OutputSuccess(combinedList)
	} else {
		w := tabwriter.NewWriter(os.Stdout, 5, 2, 3, ' ', tabwriter.TabIndent)
		var supCatalogList, unsupCatalogList []catalog.ComponentType
		var supDevfileCatalogList, unsupDevfileCatalogList []catalog.DevfileComponentType
		var supported string

		for _, image := range o.catalogList.Items {
			supported, unsupported := catalog.SliceSupportedTags(image)

			if len(supported) != 0 {
				image.Spec.NonHiddenTags = supported
				supCatalogList = append(supCatalogList, image)
			}
			if len(unsupported) != 0 {
				image.Spec.NonHiddenTags = unsupported
				unsupCatalogList = append(unsupCatalogList, image)
			}
		}

		for _, devfileComponent := range o.catalogDevfileList.Items {
			if devfileComponent.Support {
				supDevfileCatalogList = append(supDevfileCatalogList, devfileComponent)
			} else {
				unsupDevfileCatalogList = append(unsupDevfileCatalogList, devfileComponent)
			}
		}

		if len(supCatalogList) != 0 || len(unsupCatalogList) != 0 {
			fmt.Fprintln(w, "Odo OpenShift Components:")
			fmt.Fprintln(w, "NAME", "\t", "PROJECT", "\t", "TAGS", "\t", "SUPPORTED")

			if len(supCatalogList) != 0 {
				supported = "YES"
				o.printCatalogList(w, supCatalogList, supported)
			}

			if len(unsupCatalogList) != 0 {
				supported = "NO"
				o.printCatalogList(w, unsupCatalogList, supported)
			}

			fmt.Fprintln(w)
		}

		if len(supDevfileCatalogList) != 0 || (o.listAllDevfileComponents && len(unsupDevfileCatalogList) != 0) {
			fmt.Fprintln(w, "Odo Devfile Components:")
			fmt.Fprintln(w, "NAME", "\t", "DESCRIPTION", "\t", "REGISTRY", "\t", "SUPPORTED")

			if len(supDevfileCatalogList) != 0 {
				supported = "YES"
				o.printDevfileCatalogList(w, supDevfileCatalogList, supported)
			}

			if o.listAllDevfileComponents && len(unsupDevfileCatalogList) != 0 {
				supported = "NO"
				o.printDevfileCatalogList(w, unsupDevfileCatalogList, supported)
			}

			fmt.Fprintln(w)
		}

		w.Flush()
	}
	return
}

// NewCmdCatalogListComponents implements the odo catalog list components command
func NewCmdCatalogListComponents(name, fullName string) *cobra.Command {
	o := NewListComponentsOptions()

	var componentListCmd = &cobra.Command{
		Use:         name,
		Short:       "List all components",
		Long:        "List all available component types from OpenShift's Image Builder",
		Example:     fmt.Sprintf(componentsExample, fullName),
		Annotations: map[string]string{"machineoutput": "json"},
		Run: func(cmd *cobra.Command, args []string) {
			genericclioptions.GenericRun(o, cmd, args)
		},
	}

	componentListCmd.Flags().BoolVarP(&o.listAllDevfileComponents, "all", "a", false, "List both supported and unsupported devfile components.")

	return componentListCmd
}

func (o *ListComponentsOptions) printCatalogList(w io.Writer, catalogList []catalog.ComponentType, supported string) {
	for _, component := range catalogList {
		componentName := component.Name
		if component.Namespace == o.Project {
			/*
				If current namespace is same as the current component namespace,
				Loop through every other component,
				If there exists a component with same name but in different namespaces,
				mark the one from current namespace with (*)
			*/
			for _, comp := range catalogList {
				if comp.ObjectMeta.Name == component.ObjectMeta.Name && component.Namespace != comp.Namespace {
					componentName = fmt.Sprintf("%s (*)", component.ObjectMeta.Name)
				}
			}
		}
		fmt.Fprintln(w, componentName, "\t", component.ObjectMeta.Namespace, "\t", strings.Join(component.Spec.NonHiddenTags, ","), "\t", supported)
	}
}

func (o *ListComponentsOptions) printDevfileCatalogList(w io.Writer, catalogDevfileList []catalog.DevfileComponentType, supported string) {
	for _, devfileComponent := range catalogDevfileList {
		fmt.Fprintln(w, devfileComponent.Name, "\t", devfileComponent.Description, "\t", devfileComponent.Registry.Name, "\t", supported)
	}
}
