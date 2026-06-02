package model

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestPanelLayoutSnapshot is a lightweight smoke render that confirms the
// Direction-B paneled layout assembles cleanly at a realistic terminal size:
// top bar above the body, KPI strip not chopped, pods panel + side panels
// don't overlap, and the footer chips render.
func TestPanelLayoutSnapshot(t *testing.T) {
	style, err := ParseStyle("#04B575,#FFFF00,#FF0000")
	if err != nil {
		t.Fatal(err)
	}
	uiModel := NewPodsUIModel("cpu=dsc", style)
	uiModel.width = 180
	uiModel.height = 40
	uiModel.SetContextName("prod-cluster")
	uiModel.SetNamespace("staging")
	uiModel.SetResources([]string{string(v1.ResourceCPU), string(v1.ResourceMemory)})

	node := NewNode(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status: v1.NodeStatus{
			Allocatable: v1.ResourceList{
				v1.ResourceCPU:    resource.MustParse("4"),
				v1.ResourceMemory: resource.MustParse("8Gi"),
			},
		},
	})
	node.Show()
	uiModel.Cluster().AddNode(node)

	addPod := func(name string, cpu, mem string) {
		pod := NewPod(&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: name},
			Spec: v1.PodSpec{
				NodeName: "node-a",
				Containers: []v1.Container{{
					Name: "app",
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{v1.ResourceCPU: resource.MustParse("500m"), v1.ResourceMemory: resource.MustParse("256Mi")},
						Limits:   v1.ResourceList{v1.ResourceCPU: resource.MustParse("1"), v1.ResourceMemory: resource.MustParse("512Mi")},
					},
				}},
			},
			Status: v1.PodStatus{Phase: v1.PodRunning},
		})
		pod.SetUsage(v1.ResourceList{v1.ResourceCPU: resource.MustParse(cpu), v1.ResourceMemory: resource.MustParse(mem)})
		uiModel.Cluster().AddPod(pod)
	}
	addPod("api-0", "750m", "320Mi")
	addPod("worker-0", "200m", "180Mi")

	out := uiModel.View()
	for _, want := range []string{
		"k8s-pods-viewer",
		"Context: prod-cluster",
		"Namespace: staging",
		"PODS",
		"UNHEALTHY",
		"CLUSTER CPU",
		"CLUSTER MEMORY",
		"Shown:",
		"warn",
		"PODS · GROUPED BY NAMESPACE",
		"NODE PRESSURE",
		"HIGHLIGHTS",
		"nav",
		"actions",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("View() missing %q\n%s", want, out)
		}
	}

	// No line should exceed the configured width (after stripping ANSI). Allow
	// a small fudge factor for the right-pane separator/spacing.
	for i, line := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(line); w > uiModel.width+2 {
			t.Fatalf("line %d wider than terminal (%d > %d): %q", i, w, uiModel.width+2, line)
		}
	}

	if testing.Verbose() {
		fmt.Println(out)
	}
}

// TestPopupOverlay verifies the action popup floats over the pod list without
// changing the total line count (sidebar stays fully visible alongside it).
func TestPopupOverlay(t *testing.T) {
	style, err := ParseStyle("#04B575,#FFFF00,#FF0000")
	if err != nil {
		t.Fatal(err)
	}
	uiModel := NewPodsUIModel("cpu=dsc", style)
	uiModel.width = 180
	uiModel.height = 40
	uiModel.SetContextName("prod-cluster")
	uiModel.SetNamespace("staging")
	uiModel.SetResources([]string{string(v1.ResourceCPU), string(v1.ResourceMemory)})

	node := NewNode(&v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status: v1.NodeStatus{
			Allocatable: v1.ResourceList{
				v1.ResourceCPU:    resource.MustParse("4"),
				v1.ResourceMemory: resource.MustParse("8Gi"),
			},
		},
	})
	node.Show()
	uiModel.Cluster().AddNode(node)
	addPod := func(name string, cpu, mem string) {
		pod := NewPod(&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: name},
			Spec: v1.PodSpec{
				NodeName: "node-a",
				Containers: []v1.Container{{
					Name: "app",
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{v1.ResourceCPU: resource.MustParse("500m"), v1.ResourceMemory: resource.MustParse("256Mi")},
						Limits:   v1.ResourceList{v1.ResourceCPU: resource.MustParse("1"), v1.ResourceMemory: resource.MustParse("512Mi")},
					},
				}},
			},
			Status: v1.PodStatus{Phase: v1.PodRunning},
		})
		pod.SetUsage(v1.ResourceList{v1.ResourceCPU: resource.MustParse(cpu), v1.ResourceMemory: resource.MustParse(mem)})
		uiModel.Cluster().AddPod(pod)
	}
	// Add enough pods that the combined body is taller than the popup (~18 lines).
	addPod("api-0", "750m", "320Mi")
	addPod("worker-0", "200m", "180Mi")
	addPod("cache-0", "120m", "128Mi")
	addPod("queue-0", "60m", "96Mi")
	addPod("proxy-0", "30m", "64Mi")
	addPod("monitor-0", "10m", "32Mi")

	// Open the action menu on the first pod
	uiModel.Update(tea.KeyMsg{Type: tea.KeyEnter})

	outWithPopup := uiModel.View()

	// Popup content must be present
	if !strings.Contains(outWithPopup, "POD ACTIONS") {
		t.Fatalf("popup missing POD ACTIONS title")
	}
	// Sidebar must still be present (not replaced)
	if !strings.Contains(outWithPopup, "NODE PRESSURE") {
		t.Fatalf("sidebar NODE PRESSURE missing when popup is open")
	}
	if !strings.Contains(outWithPopup, "HIGHLIGHTS") {
		t.Fatalf("sidebar HIGHLIGHTS missing when popup is open")
	}

	// Line count must not change compared to view without popup
	uiModel.actionMenuOpen = false
	outWithout := uiModel.View()
	uiModel.actionMenuOpen = true

	linesWithPopup := strings.Count(outWithPopup, "\n")
	linesWithout := strings.Count(outWithout, "\n")
	if linesWithPopup != linesWithout {
		t.Fatalf("popup changed line count: with=%d without=%d\nwith popup:\n%s", linesWithPopup, linesWithout, outWithPopup)
	}

	if testing.Verbose() {
		fmt.Println(outWithPopup)
	}
}
