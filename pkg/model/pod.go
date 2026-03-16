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
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

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
