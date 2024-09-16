package kuberneteshelpers

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/utils/ptr"
)

var runtimeScheme = runtime.NewScheme()

func init() {
	utilruntime.Must(appsv1.AddToScheme(runtimeScheme))
}

func ResolveObject(kubeconfig string, namespace string, targetObject []string) (runtime.Object, error) {
	clientGetter := &genericclioptions.ConfigFlags{KubeConfig: ptr.To(kubeconfig)}
	resourceResult := resource.NewBuilder(clientGetter).
		WithScheme(runtimeScheme, runtimeScheme.PrioritizedVersionsAllGroups()...).
		NamespaceParam(namespace).DefaultNamespace().
		ResourceTypeOrNameArgs(true, targetObject...).
		SingleResourceType().
		RequireObject(true).
		Flatten().
		Do()

	if err := resourceResult.Err(); err != nil {
		return nil, fmt.Errorf("no targets found: %w", err)
	}

	if !resourceResult.TargetsSingleItems() {
		return nil, fmt.Errorf("no targets found")
	}

	object, err := resourceResult.Object()
	if err != nil {
		return nil, fmt.Errorf("unable to fetch target resource: %w", err)
	}

	return object, nil
}
