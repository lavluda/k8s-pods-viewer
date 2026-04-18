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

package main

import (
	"testing"
)

func TestPrepareRuntimeConfigSuccess(t *testing.T) {
	runtime, err := prepareRuntimeConfig(Flags{
		NodeSelector: "topology.kubernetes.io/zone=az1",
		PodSelector:  "app=api",
		Namespace:    "all",
		Resources:    "cpu, memory ,ephemeral-storage",
		Style:        "#04B575,#FFFF00,#FF0000",
	})
	if err != nil {
		t.Fatalf("prepareRuntimeConfig() error = %v", err)
	}

	if got, want := runtime.nodeSelector.String(), "topology.kubernetes.io/zone=az1"; got != want {
		t.Fatalf("nodeSelector = %q, want %q", got, want)
	}
	if got, want := runtime.podSelector.String(), "app=api"; got != want {
		t.Fatalf("podSelector = %q, want %q", got, want)
	}
	if got, want := len(runtime.resources), 3; got != want {
		t.Fatalf("len(resources) = %d, want %d", got, want)
	}
	if runtime.style == nil {
		t.Fatalf("style = nil, want non-nil")
	}
	if got, want := runtime.namespace, ""; got != want {
		t.Fatalf("namespace = %q, want %q", got, want)
	}
}

func TestPrepareRuntimeConfigInvalidStyle(t *testing.T) {
	_, err := prepareRuntimeConfig(Flags{
		Style: "#04B575,#FFFF00",
	})
	if err == nil {
		t.Fatalf("prepareRuntimeConfig() error = nil, want non-nil")
	}
}

func TestPrepareRuntimeConfigInvalidNodeSelector(t *testing.T) {
	_, err := prepareRuntimeConfig(Flags{
		Style:        "#04B575,#FFFF00,#FF0000",
		NodeSelector: "bad selector",
	})
	if err == nil {
		t.Fatalf("prepareRuntimeConfig() error = nil, want non-nil")
	}
}

func TestPrepareRuntimeConfigInvalidPodSelector(t *testing.T) {
	_, err := prepareRuntimeConfig(Flags{
		Style:       "#04B575,#FFFF00,#FF0000",
		PodSelector: "bad selector",
	})
	if err == nil {
		t.Fatalf("prepareRuntimeConfig() error = nil, want non-nil")
	}
}
