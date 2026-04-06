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
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type PodHealthSeverity int

const (
	PodHealthHealthy PodHealthSeverity = iota
	PodHealthWarning
	PodHealthCritical
)

type PodHealth struct {
	Icon     string
	Label    string
	Severity PodHealthSeverity
}

// Pod is our pod model used for internal storage and display
type Pod struct {
	mu    sync.RWMutex
	pod   v1.Pod
	usage v1.ResourceList
}

// NewPod constructs a pod model based off of the K8s pod object
func NewPod(n *v1.Pod) *Pod {
	return &Pod{
		pod:   *n,
		usage: v1.ResourceList{},
	}
}

// Update updates the pod model, replacing it with a shallow copy of the provided pod
func (p *Pod) Update(pod *v1.Pod) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pod = *pod
}

// IsScheduled returns true if the pod has been scheduled to a node
func (p *Pod) IsScheduled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.pod.Spec.NodeName != ""
}

// NodeName returns the node that the pod is scheduled against, or an empty string
func (p *Pod) NodeName() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.pod.Spec.NodeName
}

// Namespace returns the namespace of the pod
func (p *Pod) Namespace() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.pod.Namespace
}

// Name returns the name of the pod
func (p *Pod) Name() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.pod.Name
}

// FullName returns namespace/name.
func (p *Pod) FullName() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return fmt.Sprintf("%s/%s", p.pod.Namespace, p.pod.Name)
}

// Phase returns the pod phase
func (p *Pod) Phase() v1.PodPhase {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.pod.Status.Phase
}

func (p *Pod) Restarts() int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	restarts := 0
	for _, status := range p.pod.Status.InitContainerStatuses {
		restarts += int(status.RestartCount)
	}
	for _, status := range p.pod.Status.ContainerStatuses {
		restarts += int(status.RestartCount)
	}
	return restarts
}

func (p *Pod) Workload() (kind, name string) {
	p.mu.RLock()
	ownerRefs := append([]metav1.OwnerReference(nil), p.pod.OwnerReferences...)
	labels := map[string]string{}
	for k, v := range p.pod.Labels {
		labels[k] = v
	}
	name = p.pod.Name
	p.mu.RUnlock()

	if owner := controllerOwnerRef(ownerRefs); owner != nil {
		kind = owner.Kind
		name = owner.Name
		if owner.Kind == "ReplicaSet" {
			if deploymentName, ok := deploymentNameFromReplicaSet(owner.Name); ok {
				return "Deployment", deploymentName
			}
		}
		return kind, name
	}

	if appName := firstNonEmpty(labels["app.kubernetes.io/name"], labels["app"]); appName != "" {
		return "App", appName
	}
	return "Pod", name
}

func (p *Pod) Health() PodHealth {
	p.mu.RLock()
	phase := p.pod.Status.Phase
	initStatuses := append([]v1.ContainerStatus(nil), p.pod.Status.InitContainerStatuses...)
	containerStatuses := append([]v1.ContainerStatus(nil), p.pod.Status.ContainerStatuses...)
	p.mu.RUnlock()

	for _, status := range append(initStatuses, containerStatuses...) {
		if waiting := status.State.Waiting; waiting != nil {
			label := shortStatusReason(waiting.Reason)
			if isCriticalReason(waiting.Reason) {
				return PodHealth{Icon: "✕", Label: label, Severity: PodHealthCritical}
			}
			return PodHealth{Icon: "◐", Label: label, Severity: PodHealthWarning}
		}
		if terminated := status.State.Terminated; terminated != nil {
			label := shortStatusReason(terminated.Reason)
			if terminated.ExitCode != 0 || isCriticalReason(terminated.Reason) {
				return PodHealth{Icon: "✕", Label: label, Severity: PodHealthCritical}
			}
			return PodHealth{Icon: "◐", Label: label, Severity: PodHealthWarning}
		}
		if terminated := status.LastTerminationState.Terminated; terminated != nil && status.RestartCount > 0 {
			label := shortStatusReason(terminated.Reason)
			if label == "" {
				label = "Restarting"
			}
			if terminated.Reason == "OOMKilled" {
				return PodHealth{Icon: "✕", Label: "OOMKilled", Severity: PodHealthCritical}
			}
			return PodHealth{Icon: "↻", Label: label, Severity: PodHealthWarning}
		}
	}

	switch phase {
	case v1.PodRunning:
		if p.Restarts() > 0 {
			return PodHealth{Icon: "↻", Label: "Restarting", Severity: PodHealthWarning}
		}
		return PodHealth{Icon: "●", Label: "Running", Severity: PodHealthHealthy}
	case v1.PodPending:
		return PodHealth{Icon: "◐", Label: "Pending", Severity: PodHealthWarning}
	case v1.PodSucceeded:
		return PodHealth{Icon: "●", Label: "Succeeded", Severity: PodHealthHealthy}
	case v1.PodFailed:
		return PodHealth{Icon: "✕", Label: "Failed", Severity: PodHealthCritical}
	default:
		return PodHealth{Icon: "◐", Label: "Unknown", Severity: PodHealthWarning}
	}
}

// Created returns the pod creation time.
func (p *Pod) Created() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.pod.CreationTimestamp.Time
}

// Requested returns the sum of the resources requested by the pod.
// Also include resources for init containers that are sidecars as described in
// https://kubernetes.io/blog/2023/08/25/native-sidecar-containers .
func (p *Pod) Requested() v1.ResourceList {
	p.mu.RLock()
	defer p.mu.RUnlock()
	requested := v1.ResourceList{}
	for _, c := range p.pod.Spec.InitContainers {
		if c.RestartPolicy == nil || *c.RestartPolicy != v1.ContainerRestartPolicyAlways {
			continue
		}
		for rn, q := range c.Resources.Requests {
			existing := requested[rn]
			existing.Add(q)
			requested[rn] = existing
		}
	}
	for _, c := range p.pod.Spec.Containers {
		for rn, q := range c.Resources.Requests {
			existing := requested[rn]
			existing.Add(q)
			requested[rn] = existing
		}
	}
	requested[v1.ResourcePods] = resource.MustParse("1")
	return requested
}

// Limits returns the sum of the resources limited by the pod.
// Includes sidecar-style init containers.
func (p *Pod) Limits() v1.ResourceList {
	p.mu.RLock()
	defer p.mu.RUnlock()
	limits := v1.ResourceList{}
	for _, c := range p.pod.Spec.InitContainers {
		if c.RestartPolicy == nil || *c.RestartPolicy != v1.ContainerRestartPolicyAlways {
			continue
		}
		for rn, q := range c.Resources.Limits {
			existing := limits[rn]
			existing.Add(q)
			limits[rn] = existing
		}
	}
	for _, c := range p.pod.Spec.Containers {
		for rn, q := range c.Resources.Limits {
			existing := limits[rn]
			existing.Add(q)
			limits[rn] = existing
		}
	}
	return limits
}

func (p *Pod) SetUsage(usage v1.ResourceList) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.usage = v1.ResourceList{}
	for rn, q := range usage {
		p.usage[rn] = q.DeepCopy()
	}
}

func (p *Pod) ClearUsage() {
	p.SetUsage(v1.ResourceList{})
}

func (p *Pod) Usage() v1.ResourceList {
	p.mu.RLock()
	defer p.mu.RUnlock()
	usage := v1.ResourceList{}
	for rn, q := range p.usage {
		usage[rn] = q.DeepCopy()
	}
	return usage
}

func controllerOwnerRef(ownerRefs []metav1.OwnerReference) *metav1.OwnerReference {
	for _, owner := range ownerRefs {
		if owner.Controller != nil && *owner.Controller {
			ownerCopy := owner
			return &ownerCopy
		}
	}
	if len(ownerRefs) == 0 {
		return nil
	}
	owner := ownerRefs[0]
	return &owner
}

func deploymentNameFromReplicaSet(name string) (string, bool) {
	idx := strings.LastIndexByte(name, '-')
	if idx <= 0 || idx == len(name)-1 {
		return "", false
	}

	suffix := name[idx+1:]
	if len(suffix) < 6 || len(suffix) > 10 {
		return "", false
	}
	for _, ch := range suffix {
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') {
			return "", false
		}
	}
	return name[:idx], true
}

func isCriticalReason(reason string) bool {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return false
	}
	if strings.Contains(reason, "BackOff") || strings.Contains(reason, "Error") {
		return true
	}
	switch reason {
	case "CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull", "CreateContainerConfigError", "OOMKilled", "RunContainerError":
		return true
	default:
		return false
	}
}

func shortStatusReason(reason string) string {
	switch reason {
	case "":
		return ""
	case "CrashLoopBackOff":
		return "CrashLoop"
	case "ImagePullBackOff", "ErrImagePull":
		return "ImagePull"
	case "CreateContainerConfigError":
		return "ConfigError"
	default:
		return reason
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
