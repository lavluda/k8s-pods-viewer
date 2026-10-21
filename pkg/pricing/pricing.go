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

// Package pricing describes what a cluster's nodes cost per hour. It holds only
// the provider-neutral types so the UI can depend on it without pulling in a
// cloud SDK; concrete providers live in subpackages such as pricing/awsprice.
package pricing

import "strings"

// HoursPerMonth is the hours-per-month convention AWS and the other providers
// use when they quote monthly prices (365 days / 12 months * 24 hours).
const HoursPerMonth = 730

// Capacity types as they appear on Kubernetes nodes after normalization by
// NodeDetails.
const (
	CapacitySpot     = "spot"
	CapacityOnDemand = "on-demand"
)

// NodeSpec is everything a provider needs to price one node.
type NodeSpec struct {
	// InstanceType is the machine size, e.g. `m5.xlarge`.
	InstanceType string
	// Region is the provider region, e.g. `eu-west-1`. When empty it is derived
	// from Zone.
	Region string
	// Zone is the availability zone, e.g. `eu-west-1a`. Spot prices are per zone.
	Zone string
	// CapacityType is `spot`, `on-demand`, or empty when the node does not say,
	// in which case on-demand is assumed.
	CapacityType string
	// OS is the node operating system, e.g. `linux`.
	OS string
}

// Spot reports whether the node runs on spot/preemptible capacity.
func (s NodeSpec) Spot() bool {
	return strings.HasPrefix(strings.ToLower(s.CapacityType), CapacitySpot)
}

// EffectiveRegion is Region when set, otherwise the region implied by Zone.
func (s NodeSpec) EffectiveRegion() string {
	if s.Region != "" {
		return s.Region
	}
	return RegionFromZone(s.Zone)
}

// Priceable reports whether the spec carries enough metadata to look a price up.
func (s NodeSpec) Priceable() bool {
	return s.InstanceType != "" && s.EffectiveRegion() != ""
}

// Provider resolves hourly USD prices for nodes. Implementations are used from
// the render loop, so HourlyPrice must not block on network calls: it answers
// from cache and lets the provider fetch missing prices in the background.
type Provider interface {
	// HourlyPrice returns the hourly USD price for the node, and false when the
	// price is not known (yet).
	HourlyPrice(spec NodeSpec) (float64, bool)
	// Unavailable returns a short human-readable reason when the provider cannot
	// price anything at all, or "" when pricing is working.
	Unavailable() string
}

// RegionFromZone turns an availability zone into its region by dropping the
// trailing zone letter, e.g. `eu-west-1a` becomes `eu-west-1`. Local zones such
// as `us-west-2-lax-1a` keep their prefix, which is what the pricing APIs use.
func RegionFromZone(zone string) string {
	zone = strings.TrimSpace(zone)
	if zone == "" {
		return ""
	}
	last := zone[len(zone)-1]
	if last >= 'a' && last <= 'z' {
		return strings.TrimRight(zone[:len(zone)-1], "-")
	}
	return zone
}

// Static is a Provider backed by a fixed instance-type price table. It is used
// by tests and as a stand-in when live pricing is not wired up.
type Static struct {
	// OnDemand maps instance type to hourly USD price.
	OnDemand map[string]float64
	// Spot maps instance type to hourly USD spot price. Missing entries fall
	// back to the on-demand price.
	Spot map[string]float64
	// Reason, when set, is returned by Unavailable.
	Reason string
}

// HourlyPrice implements Provider.
func (s *Static) HourlyPrice(spec NodeSpec) (float64, bool) {
	if !spec.Priceable() {
		return 0, false
	}
	if spec.Spot() {
		if price, ok := s.Spot[spec.InstanceType]; ok {
			return price, true
		}
	}
	price, ok := s.OnDemand[spec.InstanceType]
	return price, ok
}

// Unavailable implements Provider.
func (s *Static) Unavailable() string {
	return s.Reason
}
