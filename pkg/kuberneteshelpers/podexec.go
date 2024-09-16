package kuberneteshelpers

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/kubernetes/scheme"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

func FileContents(ctx context.Context, config *rest.Config, pod *corev1.Pod, container, path string) (string, error) {
	client, err := corev1client.NewForConfig(config)
	if err != nil {
		return "", err
	}

	req := client.RESTClient().Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("exec")

	req.VersionedParams(&corev1.PodExecOptions{
		Container: container,
		Command:   []string{"sh", "-c", fmt.Sprintf("cat %s", path)},
		Stdin:     false,
		Stdout:    true,
		Stderr:    false,
		TTY:       false,
	}, scheme.ParameterCodec)

	websocketExec, err := remotecommand.NewWebSocketExecutor(config, "GET", req.URL().String())
	if err != nil {
		return "", err
	}

	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return "", err
	}

	exec, err = remotecommand.NewFallbackExecutor(websocketExec, exec, httpstream.IsUpgradeFailure)
	if err != nil {
		return "", err
	}

	var out strings.Builder

	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:             nil,
		Stdout:            &out,
		Stderr:            nil,
		Tty:               false,
		TerminalSizeQueue: nil,
	})
	if err != nil {
		return "", err
	}

	return out.String(), nil
}
