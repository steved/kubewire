package kuberneteshelpers

import (
	"context"
	"net/netip"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func TestGetClusterDetails(t *testing.T) {
	namespace := "test-ns"

	validPod := &corev1.Pod{
		ObjectMeta: v1.ObjectMeta{Name: "test-pod", Namespace: namespace},
		Spec:       corev1.PodSpec{},
		Status:     corev1.PodStatus{PodIP: "100.64.0.1"},
	}

	kubeDNS := &corev1.Service{
		ObjectMeta: v1.ObjectMeta{Name: "kube-dns", Namespace: "kube-system"},
		Spec: corev1.ServiceSpec{
			ClusterIP: "172.0.0.1",
		},
	}

	validNode := &corev1.Node{
		ObjectMeta: v1.ObjectMeta{Name: "node-1"},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{
				{
					Type:    corev1.NodeExternalIP,
					Address: "34.123.34.12",
				},
				{
					Type:    corev1.NodeInternalIP,
					Address: "10.0.0.1",
				},
			},
		},
	}

	validClusterDetails := ClusterDetails{
		ServiceIP:   netip.MustParseAddr("172.0.0.1"),
		PodCIDR:     netip.MustParsePrefix("100.64.0.0/16"),
		ServiceCIDR: netip.MustParsePrefix("172.0.0.0/16"),
		NodeCIDR:    netip.MustParsePrefix("10.0.0.0/16"),
	}

	tests := []struct {
		name    string
		objects []runtime.Object
		input   ClusterDetails
		want    ClusterDetails
		wantErr bool
	}{
		{
			"only host network pods",
			[]runtime.Object{
				&corev1.Pod{
					ObjectMeta: v1.ObjectMeta{Name: "test-pod-host-network", Namespace: namespace},
					Spec:       corev1.PodSpec{HostNetwork: true},
					Status:     corev1.PodStatus{},
				},
				kubeDNS,
				validNode,
			},
			ClusterDetails{},
			ClusterDetails{},
			true,
		},
		{
			"only pods with no IP",
			[]runtime.Object{
				&corev1.Pod{
					ObjectMeta: v1.ObjectMeta{Name: "test-pod-no-pod-ip", Namespace: namespace},
					Spec:       corev1.PodSpec{},
					Status:     corev1.PodStatus{},
				},
				kubeDNS,
				validNode,
			},
			ClusterDetails{},
			ClusterDetails{},
			true,
		},
		{
			"only pods with invalid IP",
			[]runtime.Object{
				&corev1.Pod{
					ObjectMeta: v1.ObjectMeta{Name: "test-pod-invalid-ip", Namespace: namespace},
					Spec:       corev1.PodSpec{},
					Status:     corev1.PodStatus{PodIP: "100."},
				},
				kubeDNS,
				validNode,
			},
			ClusterDetails{},
			ClusterDetails{},
			true,
		},
		{
			"pods with invalid IP",
			[]runtime.Object{
				&corev1.Pod{
					ObjectMeta: v1.ObjectMeta{Name: "test-pod-invalid-ip", Namespace: namespace},
					Spec:       corev1.PodSpec{},
					Status:     corev1.PodStatus{PodIP: "100."},
				},
				validPod,
				kubeDNS,
				validNode,
			},
			ClusterDetails{},
			validClusterDetails,
			false,
		},
		{
			"no kube-dns",
			[]runtime.Object{validPod, validNode},
			ClusterDetails{},
			ClusterDetails{},
			true,
		},
		{
			"kube-dns with invalid ClusterIP",
			[]runtime.Object{
				&corev1.Service{
					ObjectMeta: v1.ObjectMeta{Name: "kube-dns", Namespace: "kube-system"},
					Spec: corev1.ServiceSpec{
						ClusterIP: "",
					},
				},
				validPod,
				validNode,
			},
			ClusterDetails{},
			ClusterDetails{},
			true,
		},
		{
			"no nodes",
			[]runtime.Object{validPod, kubeDNS},
			ClusterDetails{},
			ClusterDetails{},
			true,
		},
		{
			"only nodes with external IP",
			[]runtime.Object{
				validPod,
				kubeDNS,
				&corev1.Node{
					ObjectMeta: v1.ObjectMeta{Name: "node-1"},
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{{Type: corev1.NodeExternalIP, Address: "34.123.34.12"}},
					},
				},
			},
			ClusterDetails{},
			ClusterDetails{},
			true,
		},
		{
			"only nodes with invalid IP",
			[]runtime.Object{
				validPod,
				kubeDNS,
				&corev1.Node{
					ObjectMeta: v1.ObjectMeta{Name: "node-1"},
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "100."}},
					},
				},
			},
			ClusterDetails{},
			ClusterDetails{},
			true,
		},
		{
			"nodes with invalid IP",
			[]runtime.Object{
				validPod,
				kubeDNS,
				&corev1.Node{
					ObjectMeta: v1.ObjectMeta{Name: "node-0"},
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "100."}},
					},
				},
				validNode,
			},
			ClusterDetails{},
			validClusterDetails,
			false,
		},
		{
			"valid cluster",
			[]runtime.Object{validPod, kubeDNS, validNode},
			ClusterDetails{},
			validClusterDetails,
			false,
		},
		{
			"prefilled CIDRs",
			[]runtime.Object{kubeDNS},
			ClusterDetails{
				PodCIDR:     netip.MustParsePrefix("100.64.0.0/24"),
				ServiceCIDR: netip.MustParsePrefix("172.0.0.0/12"),
				NodeCIDR:    netip.MustParsePrefix("10.0.0.0/12"),
			},
			ClusterDetails{
				ServiceIP:   netip.MustParseAddr("172.0.0.1"),
				PodCIDR:     netip.MustParsePrefix("100.64.0.0/24"),
				ServiceCIDR: netip.MustParsePrefix("172.0.0.0/12"),
				NodeCIDR:    netip.MustParsePrefix("10.0.0.0/12"),
			},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.input.Resolve(context.Background(), fake.NewClientset(tt.objects...), namespace)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetClusterDetails() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetClusterDetails() got = %v, want %v", got, tt.want)
			}
		})
	}
}
