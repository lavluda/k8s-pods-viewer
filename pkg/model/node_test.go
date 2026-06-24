/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package model_test

import (
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lavluda/k8s-pods-viewer/pkg/model"
)

func testNode(name string) *v1.Node {
	n := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: v1.NodeStatus{
			Phase: v1.NodePending,
		},
	}
	return n
}

func TestNewNode(t *testing.T) {
	n := testNode("mynode")
	node := model.NewNode(n)
	if exp, got := "mynode", node.Name(); exp != got {
		t.Errorf("expected Name == %s, got %s", exp, got)
	}
}

func TestNodeReady(t *testing.T) {
	n := testNode("mynode")
	n.Status.Conditions = append(n.Status.Conditions, v1.NodeCondition{
		Type:   v1.NodeReady,
		Status: v1.ConditionTrue,
	})
	node := model.NewNode(n)
	if !node.Ready() {
		t.Errorf("expected node to be ready")
	}
}

func TestNodeNotReady(t *testing.T) {
	for _, status := range []v1.ConditionStatus{v1.ConditionFalse, v1.ConditionUnknown} {
		t.Run(string(status), func(t *testing.T) {
			n := testNode("mynode")
			n.Status.Phase = v1.NodeRunning
			n.Status.Conditions = append(n.Status.Conditions, v1.NodeCondition{
				Type:   v1.NodeReady,
				Status: status,
			})
			node := model.NewNode(n)
			if node.Ready() {
				t.Errorf("expected node to be not ready")
			}
		})
	}
}

func TestNodeCordoned(t *testing.T) {
	n := testNode("mynode")
	n.Spec.Unschedulable = true
	node := model.NewNode(n)
	if !node.Cordoned() {
		t.Errorf("expected node to be cordoned")
	}
}

func TestNodeNotCordoned(t *testing.T) {
	n := testNode("mynode")
	node := model.NewNode(n)
	if node.Cordoned() {
		t.Errorf("expected node to not be cordoned")
	}
}

func TestNodeDetailsEKS(t *testing.T) {
	n := testNode("mynode")
	n.Spec.ProviderID = "aws:///us-east-1a/i-123"
	n.Labels = map[string]string{
		"node.kubernetes.io/instance-type": "t3.medium",
		"topology.kubernetes.io/region":    "us-east-1",
		"topology.kubernetes.io/zone":      "us-east-1a",
		"kubernetes.io/os":                 "linux",
		"kubernetes.io/arch":               "amd64",
		"eks.amazonaws.com/nodegroup":      "workers",
		"eks.amazonaws.com/capacityType":   "SPOT",
	}
	n.Status.NodeInfo = v1.NodeSystemInfo{
		OSImage:                 "Amazon Linux 2023",
		KubeletVersion:          "v1.32.1-eks",
		ContainerRuntimeVersion: "containerd://1.7.27",
	}

	got := model.NewNode(n).Details()
	if got.Platform != "EKS" || got.InstanceType != "t3.medium" || got.Pool != "workers" || got.CapacityType != "spot" {
		t.Fatalf("Details() = %#v", got)
	}
	if got.Zone != "us-east-1a" || got.OS != "linux" || got.Architecture != "amd64" {
		t.Fatalf("Details() = %#v", got)
	}
}

func TestNodeDetailsMissingOptionalMetadata(t *testing.T) {
	got := model.NewNode(testNode("standalone")).Details()
	if got.Platform != "Kubernetes" {
		t.Fatalf("Details().Platform = %q, want Kubernetes", got.Platform)
	}
	if got.InstanceType != "" || got.Pool != "" || got.CapacityType != "" {
		t.Fatalf("Details() unexpectedly populated optional metadata: %#v", got)
	}
}

func TestNodeDetailsProviderSpecificLabels(t *testing.T) {
	tests := []struct {
		name         string
		providerID   string
		labels       map[string]string
		platform     string
		pool         string
		capacityType string
	}{
		{
			name: "GKE spot",
			labels: map[string]string{
				"cloud.google.com/gke-nodepool": "batch",
				"cloud.google.com/gke-spot":     "true",
			},
			platform:     "GKE",
			pool:         "batch",
			capacityType: "spot",
		},
		{
			name: "AKS spot",
			labels: map[string]string{
				"kubernetes.azure.com/agentpool":        "workers",
				"kubernetes.azure.com/scalesetpriority": "Spot",
			},
			platform:     "AKS",
			pool:         "workers",
			capacityType: "spot",
		},
		{
			name: "Karpenter",
			labels: map[string]string{
				"karpenter.sh/nodepool":      "general",
				"karpenter.sh/capacity-type": "reserved",
			},
			platform:     "Karpenter",
			pool:         "general",
			capacityType: "reserved",
		},
		{
			name:       "unmanaged AWS",
			providerID: "aws:///us-east-1a/i-123",
			platform:   "AWS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := testNode("node")
			n.Spec.ProviderID = tt.providerID
			n.Labels = tt.labels
			got := model.NewNode(n).Details()
			if got.Platform != tt.platform || got.Pool != tt.pool || got.CapacityType != tt.capacityType {
				t.Fatalf("Details() = %#v", got)
			}
		})
	}
}
