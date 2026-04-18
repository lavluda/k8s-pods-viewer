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
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFormatResourceQuantity(t *testing.T) {
	tests := []struct {
		name     string
		resource v1.ResourceName
		quantity string
		want     string
	}{
		{name: "cpu cores", resource: v1.ResourceCPU, quantity: "2", want: "2"},
		{name: "cpu millicores", resource: v1.ResourceCPU, quantity: "500m", want: "500m"},
		{name: "cpu nanocores", resource: v1.ResourceCPU, quantity: "4302645n", want: "5m"},
		{name: "memory unchanged", resource: v1.ResourceMemory, quantity: "512Mi", want: "512Mi"},
		{name: "memory keeps kib below threshold", resource: v1.ResourceMemory, quantity: "9999Ki", want: "9999Ki"},
		{name: "memory promotes kib at threshold", resource: v1.ResourceMemory, quantity: "10000Ki", want: "10Mi"},
		{name: "memory promotes mib at threshold", resource: v1.ResourceMemory, quantity: "10000Mi", want: "10Gi"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatResourceQuantity(tc.resource, resource.MustParse(tc.quantity))
			if got != tc.want {
				t.Fatalf("formatResourceQuantity(%s, %s) = %q, want %q", tc.resource, tc.quantity, got, tc.want)
			}
		})
	}
}

func TestPodsUIModelViewShowsTransientStatus(t *testing.T) {
	style, err := ParseStyle("#04B575,#FFFF00,#FF0000")
	if err != nil {
		t.Fatalf("ParseStyle() error = %v", err)
	}

	uiModel := NewPodsUIModel("creation=dsc", style)
	uiModel.SetTransientStatus("Kubernetes watch interrupted; reconnecting automatically...", time.Minute)

	if got := uiModel.View(); !strings.Contains(got, "Kubernetes watch interrupted; reconnecting automatically...") {
		t.Fatalf("View() missing transient status, got %q", got)
	}
}

func TestPodsUIModelViewHidesExpiredTransientStatus(t *testing.T) {
	style, err := ParseStyle("#04B575,#FFFF00,#FF0000")
	if err != nil {
		t.Fatalf("ParseStyle() error = %v", err)
	}

	uiModel := NewPodsUIModel("creation=dsc", style)
	uiModel.SetTransientStatus("stale status", time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	if got := uiModel.View(); strings.Contains(got, "stale status") {
		t.Fatalf("View() unexpectedly contained expired status, got %q", got)
	}
}

func TestFormatPodUsageSummaryUsesLimitWhenPresent(t *testing.T) {
	summary, pct := formatPodUsageSummary(
		v1.ResourceCPU,
		resource.MustParse("250m"),
		resource.MustParse("500m"),
		v1.ResourceList{v1.ResourceCPU: resource.MustParse("1000m")},
	)

	if got, want := summary, "used 250m req/lim 500m/1"; got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
	if got, want := pct, 0.25; got != want {
		t.Fatalf("pct = %v, want %v", got, want)
	}
}

func TestFormatPodUsageSummaryFallsBackToRequest(t *testing.T) {
	summary, pct := formatPodUsageSummary(
		v1.ResourceCPU,
		resource.MustParse("250m"),
		resource.MustParse("500m"),
		v1.ResourceList{},
	)

	if got, want := summary, "used 250m req/lim 500m/500m"; got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
	if got, want := pct, 0.5; got != want {
		t.Fatalf("pct = %v, want %v", got, want)
	}
}

func TestFormatPodUsageSummaryKeepsBestEffortVisible(t *testing.T) {
	summary, pct := formatPodUsageSummary(
		v1.ResourceCPU,
		resource.MustParse("2619660n"),
		resource.Quantity{},
		v1.ResourceList{},
	)

	if got, want := summary, "used 3m req/lim 0/-"; got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
	if got, want := pct, 0.0; got != want {
		t.Fatalf("pct = %v, want %v", got, want)
	}
}

func TestPodsUIModelViewShowsNamespaceGroupingAndNodeAliases(t *testing.T) {
	style, err := ParseStyle("#04B575,#FFFF00,#FF0000")
	if err != nil {
		t.Fatalf("ParseStyle() error = %v", err)
	}

	uiModel := NewPodsUIModel("cpu=dsc", style)
	uiModel.SetContextName("prod-cluster")
	uiModel.SetNamespace("all")
	uiModel.width = 120

	node := NewNode(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "ip-172-24-5-93"},
		Status: v1.NodeStatus{
			Allocatable: v1.ResourceList{
				v1.ResourceCPU:    resource.MustParse("2"),
				v1.ResourceMemory: resource.MustParse("2Gi"),
			},
		},
	})
	node.Show()
	uiModel.Cluster().AddNode(node)

	pod := NewPod(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "reporting",
			Name:      "worker-0",
		},
		Spec: v1.PodSpec{
			NodeName: "ip-172-24-5-93",
			Containers: []v1.Container{{
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("500m"),
						v1.ResourceMemory: resource.MustParse("512Mi"),
					},
					Limits: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("1"),
						v1.ResourceMemory: resource.MustParse("1Gi"),
					},
				},
			}},
		},
		Status: v1.PodStatus{Phase: v1.PodRunning},
	})
	pod.SetUsage(v1.ResourceList{
		v1.ResourceCPU:    resource.MustParse("700m"),
		v1.ResourceMemory: resource.MustParse("768Mi"),
	})
	uiModel.Cluster().AddPod(pod)

	got := uiModel.View()
	if !strings.Contains(got, "Context: prod-cluster") {
		t.Fatalf("View() missing context bar, got %q", got)
	}
	if !strings.Contains(got, "reporting") {
		t.Fatalf("View() missing namespace group header, got %q", got)
	}
	if !strings.Contains(got, "worker-0") {
		t.Fatalf("View() missing pod name, got %q", got)
	}
	if !strings.Contains(got, "node-1") {
		t.Fatalf("View() missing compact node alias, got %q", got)
	}
}

func TestPodsUIModelViewAppliesFilterQuery(t *testing.T) {
	style, err := ParseStyle("#04B575,#FFFF00,#FF0000")
	if err != nil {
		t.Fatalf("ParseStyle() error = %v", err)
	}

	uiModel := NewPodsUIModel("cpu=dsc", style)
	uiModel.filterQuery = "api"

	node := NewNode(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status: v1.NodeStatus{
			Allocatable: v1.ResourceList{v1.ResourceCPU: resource.MustParse("2")},
		},
	})
	node.Show()
	uiModel.Cluster().AddNode(node)

	newTestPod := func(name string) *Pod {
		pod := NewPod(&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      name,
			},
			Spec: v1.PodSpec{
				NodeName: "node-a",
				Containers: []v1.Container{{
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{v1.ResourceCPU: resource.MustParse("250m")},
					},
				}},
			},
			Status: v1.PodStatus{Phase: v1.PodRunning},
		})
		pod.SetUsage(v1.ResourceList{v1.ResourceCPU: resource.MustParse("100m")})
		return pod
	}

	uiModel.Cluster().AddPod(newTestPod("api-0"))
	uiModel.Cluster().AddPod(newTestPod("worker-0"))

	got := uiModel.View()
	if !strings.Contains(got, "api-0") {
		t.Fatalf("View() missing filtered pod, got %q", got)
	}
	if strings.Contains(got, "worker-0") {
		t.Fatalf("View() unexpectedly contained filtered-out pod, got %q", got)
	}
	if !strings.Contains(got, "Shown: 1") {
		t.Fatalf("View() missing filtered count, got %q", got)
	}
}

func TestPodsUIModelViewHighlightsSelectedPod(t *testing.T) {
	uiModel := newPodsUIModelForSelectionTest(t)

	got := uiModel.View()
	if !strings.Contains(got, "▶") || !strings.Contains(got, "api-0") {
		t.Fatalf("View() missing selected pod marker, got %q", got)
	}
}

func TestPodsUIModelEnterOpensActionMenu(t *testing.T) {
	uiModel := newPodsUIModelForSelectionTest(t)

	uiModel.Update(tea.KeyMsg{Type: tea.KeyEnter})

	got := uiModel.View()
	if !strings.Contains(got, "Pod Actions") {
		t.Fatalf("View() missing action menu, got %q", got)
	}
	if !strings.Contains(got, "Exec") || !strings.Contains(got, "Logs") || !strings.Contains(got, "Describe") {
		t.Fatalf("View() missing action labels, got %q", got)
	}
}

func TestPodsUIModelDownMovesSelection(t *testing.T) {
	uiModel := newPodsUIModelForSelectionTest(t)

	uiModel.Update(tea.KeyMsg{Type: tea.KeyDown})

	got := uiModel.View()
	if !strings.Contains(got, "Selected: default/worker-0") {
		t.Fatalf("View() missing updated selected pod, got %q", got)
	}
	if !strings.Contains(got, "▶") || !strings.Contains(got, "worker-0") {
		t.Fatalf("View() missing moved selection marker, got %q", got)
	}
}

func TestPodsUIModelEnterUsesMovedSelection(t *testing.T) {
	uiModel := newPodsUIModelForSelectionTest(t)

	uiModel.Update(tea.KeyMsg{Type: tea.KeyDown})
	uiModel.Update(tea.KeyMsg{Type: tea.KeyEnter})

	got := uiModel.View()
	if !strings.Contains(got, "default/worker-0") {
		t.Fatalf("View() missing selected pod identity in action menu, got %q", got)
	}
}

func TestPodsUIModelSelectionFollowsRenderedGroupedOrder(t *testing.T) {
	style, err := ParseStyle("#04B575,#FFFF00,#FF0000")
	if err != nil {
		t.Fatalf("ParseStyle() error = %v", err)
	}

	uiModel := NewPodsUIModel("name=asc", style)
	uiModel.width = 120

	node := NewNode(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status: v1.NodeStatus{
			Allocatable: v1.ResourceList{v1.ResourceCPU: resource.MustParse("2")},
		},
	})
	node.Show()
	uiModel.Cluster().AddNode(node)

	addPod := func(namespace, name string) {
		pod := NewPod(&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
			},
			Spec: v1.PodSpec{
				NodeName: "node-a",
				Containers: []v1.Container{{
					Name: "app",
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{v1.ResourceCPU: resource.MustParse("250m")},
					},
				}},
			},
			Status: v1.PodStatus{Phase: v1.PodRunning},
		})
		pod.SetUsage(v1.ResourceList{v1.ResourceCPU: resource.MustParse("100m")})
		uiModel.Cluster().AddPod(pod)
	}

	addPod("alpha", "api-0")
	addPod("beta", "api-1")
	addPod("alpha", "api-2")

	uiModel.Update(tea.KeyMsg{Type: tea.KeyDown})

	got := uiModel.View()
	if !strings.Contains(got, "Selected: alpha/api-2") {
		t.Fatalf("selection did not follow rendered grouped order, got %q", got)
	}
}

func TestPodsUIModelLogsActionPromptsForContainerWhenMultipleContainers(t *testing.T) {
	style, err := ParseStyle("#04B575,#FFFF00,#FF0000")
	if err != nil {
		t.Fatalf("ParseStyle() error = %v", err)
	}

	uiModel := NewPodsUIModel("cpu=dsc", style)
	pod := NewPod(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "api-0",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{Name: "app"},
				{Name: "sidecar"},
			},
		},
		Status: v1.PodStatus{Phase: v1.PodRunning},
	})
	uiModel.Cluster().AddPod(pod)

	uiModel.Update(tea.KeyMsg{Type: tea.KeyEnter})
	uiModel.Update(tea.KeyMsg{Type: tea.KeyDown})
	uiModel.Update(tea.KeyMsg{Type: tea.KeyEnter})

	got := uiModel.View()
	if !strings.Contains(got, "Container For Logs") {
		t.Fatalf("View() missing container selection overlay, got %q", got)
	}
	if !strings.Contains(got, "app") || !strings.Contains(got, "sidecar") {
		t.Fatalf("View() missing container names, got %q", got)
	}
}

func TestPodsUIModelActionMenuShowsScalingForDeployments(t *testing.T) {
	style, err := ParseStyle("#04B575,#FFFF00,#FF0000")
	if err != nil {
		t.Fatalf("ParseStyle() error = %v", err)
	}

	uiModel := NewPodsUIModel("cpu=dsc", style)
	pod := NewPod(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "api-0",
			OwnerReferences: []metav1.OwnerReference{{
				Kind:       "ReplicaSet",
				Name:       "api-7d9d6f7f4b",
				Controller: boolPtr(true),
			}},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{{Name: "app"}},
		},
		Status: v1.PodStatus{Phase: v1.PodRunning},
	})
	uiModel.Cluster().AddPod(pod)

	uiModel.Update(tea.KeyMsg{Type: tea.KeyEnter})

	got := uiModel.View()
	if !strings.Contains(got, "Kill Pod") {
		t.Fatalf("View() missing kill action, got %q", got)
	}
	if !strings.Contains(got, "Scale -1") || !strings.Contains(got, "Scale +1") {
		t.Fatalf("View() missing scale actions, got %q", got)
	}
}

func TestDisplayedPodActionOptionsFollowRenderedOrder(t *testing.T) {
	pod := NewPod(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "api-0",
			OwnerReferences: []metav1.OwnerReference{{
				Kind:       "ReplicaSet",
				Name:       "api-7d9d6f7f4b",
				Controller: boolPtr(true),
			}},
		},
		Status: v1.PodStatus{Phase: v1.PodRunning},
	})

	got := displayedPodActionOptions(pod)
	want := []podActionKind{
		podActionExec,
		podActionLogs,
		podActionDescribe,
		podActionScaleDown,
		podActionScaleUp,
		podActionKill,
	}

	if len(got) != len(want) {
		t.Fatalf("displayedPodActionOptions() length = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("displayedPodActionOptions()[%d] = %q, want %q", index, got[index], want[index])
		}
	}
}

func TestPodsUIModelKillActionRequiresConfirmation(t *testing.T) {
	style, err := ParseStyle("#04B575,#FFFF00,#FF0000")
	if err != nil {
		t.Fatalf("ParseStyle() error = %v", err)
	}

	uiModel := NewPodsUIModel("cpu=dsc", style)
	pod := NewPod(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "api-0",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{{Name: "app"}},
		},
		Status: v1.PodStatus{Phase: v1.PodRunning},
	})
	uiModel.Cluster().AddPod(pod)

	uiModel.Update(tea.KeyMsg{Type: tea.KeyEnter})
	uiModel.Update(tea.KeyMsg{Type: tea.KeyDown})
	uiModel.Update(tea.KeyMsg{Type: tea.KeyDown})
	uiModel.Update(tea.KeyMsg{Type: tea.KeyDown})
	uiModel.Update(tea.KeyMsg{Type: tea.KeyEnter})

	got := uiModel.View()
	if !strings.Contains(got, "Confirm Action") {
		t.Fatalf("View() missing confirmation state, got %q", got)
	}
	if !strings.Contains(got, "Delete this pod now?") {
		t.Fatalf("View() missing delete confirmation text, got %q", got)
	}
	if !strings.Contains(got, "enter Confirm") || !strings.Contains(got, "esc Cancel") {
		t.Fatalf("View() missing highlighted confirm buttons, got %q", got)
	}
}

func TestPodsUIModelConfirmButtonsSupportKeyboardNavigation(t *testing.T) {
	style, err := ParseStyle("#04B575,#FFFF00,#FF0000")
	if err != nil {
		t.Fatalf("ParseStyle() error = %v", err)
	}

	uiModel := NewPodsUIModel("cpu=dsc", style)
	pod := NewPod(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "api-0",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{{Name: "app"}},
		},
		Status: v1.PodStatus{Phase: v1.PodRunning},
	})
	uiModel.Cluster().AddPod(pod)

	uiModel.Update(tea.KeyMsg{Type: tea.KeyEnter})
	uiModel.Update(tea.KeyMsg{Type: tea.KeyDown})
	uiModel.Update(tea.KeyMsg{Type: tea.KeyDown})
	uiModel.Update(tea.KeyMsg{Type: tea.KeyDown})
	uiModel.Update(tea.KeyMsg{Type: tea.KeyEnter})

	uiModel.Update(tea.KeyMsg{Type: tea.KeyRight})
	if !uiModel.confirmActionOpen {
		t.Fatalf("confirm dialog should still be open after moving focus")
	}
	if uiModel.confirmActionIndex != 1 {
		t.Fatalf("confirmActionIndex = %d, want 1", uiModel.confirmActionIndex)
	}

	uiModel.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if uiModel.confirmActionOpen {
		t.Fatalf("confirm dialog should close after selecting cancel")
	}
	if !uiModel.actionMenuOpen {
		t.Fatalf("action menu should reopen after selecting cancel")
	}
}

func TestPodsUIModelInitialLoadStaysOnFirstPage(t *testing.T) {
	style, err := ParseStyle("#04B575,#FFFF00,#FF0000")
	if err != nil {
		t.Fatalf("ParseStyle() error = %v", err)
	}

	uiModel := NewPodsUIModel("name=asc", style)
	uiModel.width = 160
	uiModel.height = 32

	node := NewNode(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status: v1.NodeStatus{
			Allocatable: v1.ResourceList{v1.ResourceCPU: resource.MustParse("2")},
		},
	})
	node.Show()
	uiModel.Cluster().AddNode(node)

	addTestSelectionPod(uiModel, "default", "z-last")
	if got := uiModel.View(); !strings.Contains(got, "Selected: default/z-last") {
		t.Fatalf("initial selection missing, got %q", got)
	}

	for index := 0; index < 16; index++ {
		addTestSelectionPod(uiModel, "default", "a-pod-"+strconv.Itoa(index))
	}

	got := uiModel.View()
	if !strings.Contains(got, "Selected: default/a-pod-0") {
		t.Fatalf("View() did not re-anchor to the first rendered pod, got %q", got)
	}
	if !strings.Contains(got, "Page: 1/") {
		t.Fatalf("View() did not stay on the first page, got %q", got)
	}
}

func newPodsUIModelForSelectionTest(t *testing.T) *PodsUIModel {
	t.Helper()

	style, err := ParseStyle("#04B575,#FFFF00,#FF0000")
	if err != nil {
		t.Fatalf("ParseStyle() error = %v", err)
	}

	uiModel := NewPodsUIModel("name=asc", style)
	uiModel.width = 120

	node := NewNode(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status: v1.NodeStatus{
			Allocatable: v1.ResourceList{v1.ResourceCPU: resource.MustParse("2")},
		},
	})
	node.Show()
	uiModel.Cluster().AddNode(node)

	for _, name := range []string{"api-0", "worker-0"} {
		addTestSelectionPod(uiModel, "default", name)
	}

	return uiModel
}

func addTestSelectionPod(uiModel *PodsUIModel, namespace string, name string) {
	pod := NewPod(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: v1.PodSpec{
			NodeName: "node-a",
			Containers: []v1.Container{{
				Name: "app",
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{v1.ResourceCPU: resource.MustParse("250m")},
				},
			}},
		},
		Status: v1.PodStatus{Phase: v1.PodRunning},
	})
	pod.SetUsage(v1.ResourceList{v1.ResourceCPU: resource.MustParse("100m")})
	uiModel.Cluster().AddPod(pod)
}

func boolPtr(v bool) *bool {
	return &v
}
