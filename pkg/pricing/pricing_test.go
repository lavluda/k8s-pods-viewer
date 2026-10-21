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

package pricing_test

import (
	"testing"

	"github.com/lavluda/k8s-pods-viewer/pkg/pricing"
)

func TestRegionFromZone(t *testing.T) {
	tests := map[string]string{
		"eu-west-1a":       "eu-west-1",
		"us-east-1f":       "us-east-1",
		"us-west-2-lax-1a": "us-west-2-lax-1",
		"eu-west-1":        "eu-west-1",
		"":                 "",
	}
	for zone, want := range tests {
		if got := pricing.RegionFromZone(zone); got != want {
			t.Errorf("RegionFromZone(%q) = %q, want %q", zone, got, want)
		}
	}
}

func TestNodeSpecHelpers(t *testing.T) {
	spec := pricing.NodeSpec{InstanceType: "m5.large", Zone: "eu-west-1b", CapacityType: "spot"}
	if !spec.Spot() {
		t.Error("Spot() = false, want true")
	}
	if got := spec.EffectiveRegion(); got != "eu-west-1" {
		t.Errorf("EffectiveRegion() = %q, want eu-west-1", got)
	}
	if !spec.Priceable() {
		t.Error("Priceable() = false, want true")
	}

	onDemand := pricing.NodeSpec{InstanceType: "m5.large", Region: "eu-west-1", CapacityType: "on-demand"}
	if onDemand.Spot() {
		t.Error("Spot() = true for on-demand capacity")
	}

	for _, spec := range []pricing.NodeSpec{
		{Region: "eu-west-1"},
		{InstanceType: "m5.large"},
	} {
		if spec.Priceable() {
			t.Errorf("Priceable() = true for %#v", spec)
		}
	}
}

func TestStaticProviderPrefersSpotThenOnDemand(t *testing.T) {
	provider := &pricing.Static{
		OnDemand: map[string]float64{"m5.large": 0.107},
		Spot:     map[string]float64{"m5.large": 0.031},
	}

	if price, ok := provider.HourlyPrice(pricing.NodeSpec{InstanceType: "m5.large", Zone: "eu-west-1a", CapacityType: "spot"}); !ok || price != 0.031 {
		t.Fatalf("spot price = %v, %v; want 0.031, true", price, ok)
	}
	if price, ok := provider.HourlyPrice(pricing.NodeSpec{InstanceType: "m5.large", Zone: "eu-west-1a"}); !ok || price != 0.107 {
		t.Fatalf("on-demand price = %v, %v; want 0.107, true", price, ok)
	}
	// Spot capacity with no spot entry falls back to the on-demand ceiling.
	provider.Spot = nil
	if price, ok := provider.HourlyPrice(pricing.NodeSpec{InstanceType: "m5.large", Zone: "eu-west-1a", CapacityType: "spot"}); !ok || price != 0.107 {
		t.Fatalf("spot fallback = %v, %v; want 0.107, true", price, ok)
	}
	if _, ok := provider.HourlyPrice(pricing.NodeSpec{InstanceType: "unknown.type", Region: "eu-west-1"}); ok {
		t.Fatal("HourlyPrice() = ok for an unknown instance type")
	}
	if got := provider.Unavailable(); got != "" {
		t.Fatalf("Unavailable() = %q, want empty", got)
	}
}

func TestHoursPerMonth(t *testing.T) {
	if pricing.HoursPerMonth != 730 {
		t.Fatalf("HoursPerMonth = %d, want 730", pricing.HoursPerMonth)
	}
}
