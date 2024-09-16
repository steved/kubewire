package kuberneteshelpers

import (
	"fmt"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
)

func ClientConfig(kubeconfig string) (kubernetes.Interface, *rest.Config, error) {
	clientGetter := &genericclioptions.ConfigFlags{KubeConfig: ptr.To(kubeconfig)}

	restConfig, err := clientGetter.ToRESTConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("unable to create kubernetes REST config: %w", err)
	}

	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to create kubernetes client: %w", err)
	}

	return client, restConfig, nil
}
