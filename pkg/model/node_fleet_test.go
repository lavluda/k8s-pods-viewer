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

package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func fleetTestNode(name string, providerID string, labels map[string]string, info v1.NodeSystemInfo) *Node {
	node := NewNode(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec:       v1.NodeSpec{ProviderID: providerID},
		Status: v1.NodeStatus{
			NodeInfo:   info,
			Conditions: []v1.NodeCondition{{Type: v1.NodeReady, Status: v1.ConditionTrue}},
		},
	})
	node.Show()
	return node
}

func eksFleetNodes() []*Node {
	return []*Node{
		fleetTestNode("ip-10-0-1-10.eu-west-1.compute.internal", "aws:///eu-west-1a/i-0aaa", map[string]string{
			"node.kubernetes.io/instance-type":  "m5.xlarge",
			"topology.kubernetes.io/region":     "eu-west-1",
			"topology.kubernetes.io/zone":       "eu-west-1a",
			"kubernetes.io/os":                  "linux",
			"kubernetes.io/arch":                "amd64",
			"eks.amazonaws.com/nodegroup":       "general",
			"eks.amazonaws.com/capacityType":    "ON_DEMAND",
			"eks.amazonaws.com/nodegroup-image": "ami-0123456789abcdef0",
		}, v1.NodeSystemInfo{KubeletVersion: "v1.30.4-eks-a737599", OSImage: "Amazon Linux 2023", ContainerRuntimeVersion: "containerd://1.7.27"}),
		fleetTestNode("ip-10-0-2-20.eu-west-1.compute.internal", "aws:///eu-west-1b/i-0bbb", map[string]string{
			"node.kubernetes.io/instance-type": "m5.xlarge",
			"topology.kubernetes.io/region":    "eu-west-1",
			"topology.kubernetes.io/zone":      "eu-west-1b",
			"kubernetes.io/os":                 "linux",
			"kubernetes.io/arch":               "amd64",
			"karpenter.sh/nodepool":            "spot-pool",
			"karpenter.sh/capacity-type":       "spot",
		}, v1.NodeSystemInfo{KubeletVersion: "v1.30.4-eks-a737599", OSImage: "Bottlerocket OS 1.20", ContainerRuntimeVersion: "containerd://1.7.27"}),
	}
}

func TestBuildNodeFleetDetectsEKSAndRollsUpMetadata(t *testing.T) {
	fleet := BuildNodeFleet(eksFleetNodes(), map[string]string{
		"ip-10-0-1-10.eu-west-1.compute.internal": "node-1",
		"ip-10-0-2-20.eu-west-1.compute.internal": "node-2",
	})

	if fleet.Profile.Name != "EKS" {
		t.Fatalf("Profile.Name = %q, want EKS", fleet.Profile.Name)
	}
	if fleet.Profile.PoolLabel != "nodegroup" || fleet.Profile.PanelTitle != "EKS Nodes" {
		t.Fatalf("Profile = %#v", fleet.Profile)
	}
	if len(fleet.Entries) != 2 {
		t.Fatalf("len(Entries) = %d, want 2", len(fleet.Entries))
	}
	if fleet.Entries[0].Alias != "node-1" {
		t.Fatalf("Entries[0].Alias = %q, want node-1", fleet.Entries[0].Alias)
	}
	if fleet.Entries[0].Details.InstanceID != "i-0aaa" {
		t.Fatalf("Entries[0].Details.InstanceID = %q, want i-0aaa", fleet.Entries[0].Details.InstanceID)
	}
	if got := formatFleetCounts(fleet.InstanceTypes, 3); got != "m5.xlarge" {
		t.Fatalf("instance rollup = %q, want m5.xlarge", got)
	}
	if got := formatFleetCounts(fleet.CapacityTypes, 3); got != "on-demand 1 · spot 1" {
		t.Fatalf("capacity rollup = %q", got)
	}
	if got := formatFleetCounts(fleet.Pools, 3); got != "general 1 · spot-pool 1" {
		t.Fatalf("pool rollup = %q", got)
	}
	if got := formatFleetCounts(fleet.Zones, 3); got != "eu-west-1a 1 · eu-west-1b 1" {
		t.Fatalf("zone rollup = %q", got)
	}
	if got := formatFleetCounts(fleet.Architectures, 3); got != "linux/amd64" {
		t.Fatalf("arch rollup = %q", got)
	}
}

func TestNodeFleetPanelRendersPlatformColumns(t *testing.T) {
	style, err := ParseStyle("#04B575,#FFFF00,#FF0000")
	if err != nil {
		t.Fatal(err)
	}
	uiModel := NewPodsUIModel("cpu=dsc", style)
	fleet := BuildNodeFleet(eksFleetNodes(), map[string]string{
		"ip-10-0-1-10.eu-west-1.compute.internal": "node-1",
		"ip-10-0-2-20.eu-west-1.compute.internal": "node-2",
	})

	out := uiModel.renderNodeFleetPanel(fleet, 72, 20)
	for _, want := range []string{
		"EKS NODES", "2 nodes",
		"INSTANCE", "CAPACITY", "ZONE", "NODEGROUP",
		"m5.xlarge", "on-demand", "spot", "eu-west-1a", "general", "spot-pool",
		"CAPACITY  ", "KUBELET",
	} {
		if !strings.Contains(ansi.Strip(out), want) {
			t.Fatalf("panel missing %q\n%s", want, out)
		}
	}
	for i, line := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(line); w != 72 {
			t.Fatalf("line %d width = %d, want 72: %q", i, w, line)
		}
	}
	if testing.Verbose() {
		fmt.Println(out)
	}
}

func TestNodeFleetPanelFallsBackForVanillaKubernetes(t *testing.T) {
	style, err := ParseStyle("#04B575,#FFFF00,#FF0000")
	if err != nil {
		t.Fatal(err)
	}
	uiModel := NewPodsUIModel("cpu=dsc", style)
	nodes := []*Node{
		fleetTestNode("kind-control-plane", "", map[string]string{
			"kubernetes.io/os":   "linux",
			"kubernetes.io/arch": "arm64",
		}, v1.NodeSystemInfo{KubeletVersion: "v1.31.0", ContainerRuntimeVersion: "containerd://1.7.18"}),
	}

	fleet := BuildNodeFleet(nodes, nil)
	if fleet.Profile.PanelTitle != "Node Fleet" || fleet.Profile.Cloud {
		t.Fatalf("Profile = %#v", fleet.Profile)
	}

	out := ansi.Strip(uiModel.renderNodeFleetPanel(fleet, 72, 20))
	for _, want := range []string{"NODE FLEET", "1 nodes", "ARCH", "KUBELET", "linux/arm64", "v1.31.0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("panel missing %q\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"INSTANCE", "CAPACITY", "ZONE", "NODEGROUP"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("panel unexpectedly contains %q for a non-cloud cluster\n%s", unwanted, out)
		}
	}
}

func TestNodeFleetPanelRespectsLineBudget(t *testing.T) {
	style, err := ParseStyle("#04B575,#FFFF00,#FF0000")
	if err != nil {
		t.Fatal(err)
	}
	uiModel := NewPodsUIModel("cpu=dsc", style)

	nodes := make([]*Node, 0, 12)
	for i := 0; i < 12; i++ {
		nodes = append(nodes, fleetTestNode(fmt.Sprintf("ip-10-0-0-%02d", i), fmt.Sprintf("aws:///eu-west-1a/i-%02d", i), map[string]string{
			"node.kubernetes.io/instance-type": "m5.large",
			"topology.kubernetes.io/zone":      "eu-west-1a",
			"eks.amazonaws.com/nodegroup":      "general",
			"eks.amazonaws.com/capacityType":   "SPOT",
		}, v1.NodeSystemInfo{KubeletVersion: "v1.30.4-eks-a737599"}))
	}
	fleet := BuildNodeFleet(nodes, nil)

	out := uiModel.renderNodeFleetPanel(fleet, 72, 10)
	if out == "" {
		t.Fatal("panel unexpectedly empty at a 10 line budget")
	}
	if got := len(strings.Split(out, "\n")); got > 10 {
		t.Fatalf("panel used %d lines, budget was 10\n%s", got, out)
	}
	if !strings.Contains(ansi.Strip(out), "more nodes") {
		t.Fatalf("panel missing truncation notice\n%s", out)
	}

	if got := uiModel.renderNodeFleetPanel(fleet, 72, 3); got != "" {
		t.Fatalf("panel rendered with no room for rows:\n%s", got)
	}
}

func TestViewRendersPlatformNodePanel(t *testing.T) {
	style, err := ParseStyle("#04B575,#FFFF00,#FF0000")
	if err != nil {
		t.Fatal(err)
	}
	uiModel := NewPodsUIModel("cpu=dsc", style)
	uiModel.width = 190
	uiModel.height = 44
	for _, node := range eksFleetNodes() {
		uiModel.Cluster().AddNode(node)
	}

	out := ansi.Strip(uiModel.View())
	for _, want := range []string{"NODE PRESSURE", "HIGHLIGHTS", "EKS NODES", "m5.xlarge"} {
		if !strings.Contains(out, want) {
			t.Fatalf("View() missing %q\n%s", want, out)
		}
	}
	for i, line := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(line); w > uiModel.width+2 {
			t.Fatalf("line %d wider than terminal (%d > %d): %q", i, w, uiModel.width+2, line)
		}
	}
	if testing.Verbose() {
		fmt.Println(out)
	}
}
