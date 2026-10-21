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

	"github.com/lavluda/k8s-pods-viewer/pkg/pricing"
)

// ClusterCost is the running compute cost of the visible nodes. It covers node
// instances only: control-plane fees, storage, load balancers, and data transfer
// are not part of it.
type ClusterCost struct {
	HourlyUSD  float64
	MonthlyUSD float64
	// PricedNodes is how many of TotalNodes had a known price.
	PricedNodes int
	TotalNodes  int
	// Unavailable is a short reason why pricing is not working, when it is not.
	Unavailable string
}

// Known reports whether at least one node could be priced.
func (c ClusterCost) Known() bool {
	return c.PricedNodes > 0
}

// SetPricing wires a price provider into the cost tile. A nil provider hides the
// tile, which is what `--cost=false` does.
func (u *PodsUIModel) SetPricing(provider pricing.Provider) {
	u.pricing = provider
}

// clusterCost sums the hourly price of the nodes currently visible.
func (u *PodsUIModel) clusterCost(nodes []*Node) ClusterCost {
	cost := ClusterCost{TotalNodes: len(nodes)}
	if u.pricing == nil {
		return cost
	}

	supported := 0
	for _, node := range nodes {
		details := node.Details()
		if !platformHasPricing(details.Platform) {
			continue
		}
		supported++
		price, ok := u.pricing.HourlyPrice(nodePriceSpec(details))
		if !ok {
			continue
		}
		cost.HourlyUSD += price
		cost.PricedNodes++
	}
	cost.MonthlyUSD = cost.HourlyUSD * pricing.HoursPerMonth
	switch {
	case supported == 0 && len(nodes) > 0:
		cost.Unavailable = "no priced platform"
	default:
		cost.Unavailable = u.pricing.Unavailable()
	}
	return cost
}

// platformHasPricing reports whether the viewer knows how to price a platform.
// Only AWS-backed nodes are priced today, so other platforms are skipped instead
// of being sent to the AWS Pricing API as unknown instance types.
func platformHasPricing(platform string) bool {
	switch platform {
	case "EKS", "AWS":
		return true
	default:
		return false
	}
}

func nodePriceSpec(details NodeDetails) pricing.NodeSpec {
	return pricing.NodeSpec{
		InstanceType: details.InstanceType,
		Region:       details.Region,
		Zone:         details.Zone,
		CapacityType: details.CapacityType,
		OS:           details.OS,
	}
}

// formatHourlyUSD keeps small clusters readable (cents matter at $0.31/hr) while
// large ones stay narrow.
func formatHourlyUSD(usd float64) string {
	switch {
	case usd >= 100:
		return fmt.Sprintf("$%.0f/hr", usd)
	case usd >= 10:
		return fmt.Sprintf("$%.1f/hr", usd)
	default:
		return fmt.Sprintf("$%.2f/hr", usd)
	}
}

func formatMonthlyUSD(usd float64) string {
	if usd >= 10000 {
		return fmt.Sprintf("$%.1fk/mo", usd/1000)
	}
	return fmt.Sprintf("$%.0f/mo", usd)
}

// costSubLine is the tile's second row: the monthly projection and how much of
// the fleet it covers, or the reason there is no number.
func (c ClusterCost) costSubLine() string {
	if c.Known() {
		return fmt.Sprintf("%s · %d/%d nodes priced", formatMonthlyUSD(c.MonthlyUSD), c.PricedNodes, c.TotalNodes)
	}
	if c.Unavailable != "" {
		return c.Unavailable
	}
	if c.TotalNodes == 0 {
		return "waiting for nodes"
	}
	return "waiting for prices…"
}
