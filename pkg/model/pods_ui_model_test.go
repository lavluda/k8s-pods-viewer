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
	"strings"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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
