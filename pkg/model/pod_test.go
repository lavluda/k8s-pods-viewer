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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lavluda/k8s-pods-viewer/pkg/model"
)

func testPod(namespace, name string) *v1.Pod {
	restartAlways := v1.ContainerRestartPolicyAlways
	p := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Status: v1.PodStatus{
			Phase: v1.PodPending,
		},
		Spec: v1.PodSpec{
			InitContainers: []v1.Container{
				{
					Image: "normalinit",
					Name:  "container",
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU:    resource.MustParse("1"),
							v1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				},
				{
					Image: "sidecar",
					Name:  "container",
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU:    resource.MustParse("1"),
							v1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
					RestartPolicy: &restartAlways,
				},
			},
			Containers: []v1.Container{
				{
					Image: "test-image",
					Name:  "container",
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU:    resource.MustParse("1"),
							v1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				},
			},
		},
	}
	return p
}
func TestNewPod(t *testing.T) {
	pod := testPod("default", "mypod")
	pod.Spec.NodeName = "mynode"
	p := model.NewPod(pod)
	if exp, got := "default", p.Namespace(); exp != got {
		t.Errorf("expected Namespace = %s, got %s", exp, got)
	}
	if exp, got := "mypod", p.Name(); exp != got {
		t.Errorf("expected Name = %s, got %s", exp, got)
	}
	if exp, got := "mynode", p.NodeName(); exp != got {
		t.Errorf("expected NodeName = %s, got %s", exp, got)
	}
	if exp, got := true, p.IsScheduled(); exp != got {
		t.Errorf("expected IsScheduled = %v, got %v", exp, got)
	}
	if exp, got := v1.PodPending, p.Phase(); exp != got {
		t.Errorf("expected Phase = %v, got %v", exp, got)
	}

	if exp, got := resource.MustParse("2"), p.Requested()[v1.ResourceCPU]; exp.Cmp(got) != 0 {
		t.Errorf("expected CPU = %s, got %s", exp.String(), got.String())
	}
	if exp, got := resource.MustParse("2Gi"), p.Requested()[v1.ResourceMemory]; exp.Cmp(got) != 0 {
		t.Errorf("expected Memory = %s, got %s", exp.String(), got.String())
	}
}

func TestPodUpdate(t *testing.T) {
	p := model.NewPod(testPod("default", "mypod"))
	if exp, got := "", p.NodeName(); got != exp {
		t.Errorf("expected NodeName == %s, got %s", exp, got)
	}
	replacement := testPod("default", "mypod")
	replacement.Spec.NodeName = "scheduled.node"
	p.Update(replacement)
	if exp, got := "scheduled.node", p.NodeName(); got != exp {
		t.Errorf("expected NodeName == %s, got %s", exp, got)
	}
}

func TestPodWorkloadUsesDeploymentForReplicaSetOwner(t *testing.T) {
	pod := testPod("default", "mypod")
	controller := true
	pod.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: "apps/v1",
		Kind:       "ReplicaSet",
		Name:       "reporting-worker-7f9c4d7bb8",
		Controller: &controller,
	}}

	kind, name := model.NewPod(pod).Workload()
	if got, want := kind, "Deployment"; got != want {
		t.Fatalf("kind = %q, want %q", got, want)
	}
	if got, want := name, "reporting-worker"; got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
}

func TestPodHealthDetectsCrashLoop(t *testing.T) {
	pod := testPod("default", "mypod")
	pod.Status.Phase = v1.PodRunning
	pod.Status.ContainerStatuses = []v1.ContainerStatus{{
		Name: "container",
		State: v1.ContainerState{
			Waiting: &v1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
		},
	}}

	health := model.NewPod(pod).Health()
	if got, want := health.Label, "CrashLoop"; got != want {
		t.Fatalf("label = %q, want %q", got, want)
	}
	if got, want := health.Severity, model.PodHealthCritical; got != want {
		t.Fatalf("severity = %v, want %v", got, want)
	}
}

func TestPodRestartsSumsInitAndAppContainers(t *testing.T) {
	pod := testPod("default", "mypod")
	pod.Status.InitContainerStatuses = []v1.ContainerStatus{{RestartCount: 2}}
	pod.Status.ContainerStatuses = []v1.ContainerStatus{{RestartCount: 3}, {RestartCount: 1}}

	if got, want := model.NewPod(pod).Restarts(), 6; got != want {
		t.Fatalf("Restarts() = %d, want %d", got, want)
	}
}
