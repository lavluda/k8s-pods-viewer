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
	"math"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	v1 "k8s.io/api/core/v1"

	"github.com/lavluda/k8s-pods-viewer/pkg/pricing"
)

func costTestModel(t *testing.T) *PodsUIModel {
	t.Helper()
	style, err := ParseStyle("#04B575,#FFFF00,#FF0000")
	if err != nil {
		t.Fatal(err)
	}
	uiModel := NewPodsUIModel("cpu=dsc", style)
	uiModel.width = 190
	uiModel.height = 44
	return uiModel
}

func staticPrices() *pricing.Static {
	return &pricing.Static{
		OnDemand: map[string]float64{"m5.xlarge": 0.214},
		Spot:     map[string]float64{"m5.xlarge": 0.0721},
	}
}

func TestClusterCostSumsSpotAndOnDemandNodes(t *testing.T) {
	uiModel := costTestModel(t)
	uiModel.SetPricing(staticPrices())
	nodes := eksFleetNodes() // node 1 on-demand, node 2 spot

	cost := uiModel.clusterCost(nodes)
	wantHourly := 0.214 + 0.0721
	if math.Abs(cost.HourlyUSD-wantHourly) > 1e-9 {
		t.Fatalf("HourlyUSD = %v, want %v", cost.HourlyUSD, wantHourly)
	}
	if math.Abs(cost.MonthlyUSD-wantHourly*pricing.HoursPerMonth) > 1e-6 {
		t.Fatalf("MonthlyUSD = %v, want %v", cost.MonthlyUSD, wantHourly*pricing.HoursPerMonth)
	}
	if cost.PricedNodes != 2 || cost.TotalNodes != 2 {
		t.Fatalf("priced %d/%d nodes, want 2/2", cost.PricedNodes, cost.TotalNodes)
	}
	if !cost.Known() || cost.Unavailable != "" {
		t.Fatalf("cost = %#v, want known with no reason", cost)
	}
	if got := cost.costSubLine(); got != "$209/mo · 2/2 nodes priced" {
		t.Fatalf("costSubLine() = %q", got)
	}
}

func TestClusterCostReportsPartialAndUnavailablePricing(t *testing.T) {
	uiModel := costTestModel(t)
	// Only the on-demand type is priced, so the spot node stays unpriced.
	uiModel.SetPricing(&pricing.Static{OnDemand: map[string]float64{"c5a.large": 0.096}})
	nodes := eksFleetNodes()

	cost := uiModel.clusterCost(nodes)
	if cost.PricedNodes != 0 || cost.Known() {
		t.Fatalf("cost = %#v, want nothing priced", cost)
	}
	if got := cost.costSubLine(); got != "waiting for prices…" {
		t.Fatalf("costSubLine() = %q, want the waiting message", got)
	}

	uiModel.SetPricing(&pricing.Static{Reason: "no AWS credentials"})
	if got := uiModel.clusterCost(nodes).costSubLine(); got != "no AWS credentials" {
		t.Fatalf("costSubLine() = %q, want the provider reason", got)
	}
}

func TestClusterCostSkipsUnpricedPlatforms(t *testing.T) {
	uiModel := costTestModel(t)
	uiModel.SetPricing(staticPrices())

	gkeNode := fleetTestNode("gke-node-1", "gce://project/europe-west1-b/gke-node-1", map[string]string{
		"node.kubernetes.io/instance-type": "m5.xlarge",
		"topology.kubernetes.io/zone":      "europe-west1-b",
		"cloud.google.com/gke-nodepool":    "default",
	}, v1.NodeSystemInfo{})

	cost := uiModel.clusterCost([]*Node{gkeNode})
	if cost.PricedNodes != 0 {
		t.Fatalf("priced %d GKE nodes, want 0", cost.PricedNodes)
	}
	if cost.Unavailable != "no priced platform" {
		t.Fatalf("Unavailable = %q, want \"no priced platform\"", cost.Unavailable)
	}
}

func TestClusterCostWithoutProvider(t *testing.T) {
	uiModel := costTestModel(t)
	cost := uiModel.clusterCost(eksFleetNodes())
	if cost.Known() || cost.HourlyUSD != 0 || cost.TotalNodes != 2 {
		t.Fatalf("cost = %#v, want an empty cost for 2 nodes", cost)
	}
}

func TestHeaderShowsCostTileBesideClusterMemory(t *testing.T) {
	uiModel := costTestModel(t)
	uiModel.SetResources([]string{string(v1.ResourceCPU), string(v1.ResourceMemory)})
	uiModel.SetPricing(staticPrices())
	for _, node := range eksFleetNodes() {
		uiModel.Cluster().AddNode(node)
	}

	out := ansi.Strip(uiModel.View())
	for _, want := range []string{"CLUSTER MEMORY", "CLUSTER COST", "$0.29/hr", "$209/mo · 2/2 nodes priced"} {
		if !strings.Contains(out, want) {
			t.Fatalf("View() missing %q\n%s", want, out)
		}
	}

	memoryLabelIndex := strings.Index(out, "CLUSTER MEMORY")
	costLabelIndex := strings.Index(out, "CLUSTER COST")
	if costLabelIndex < memoryLabelIndex {
		t.Fatalf("cost tile renders before the memory tile\n%s", out)
	}
	if headerLine := strings.Split(out, "\n")[1]; !strings.Contains(headerLine, "CLUSTER MEMORY") || !strings.Contains(headerLine, "CLUSTER COST") {
		t.Fatalf("cost tile is not on the KPI strip row: %q", headerLine)
	}
	for i, line := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(line); w > uiModel.width+2 {
			t.Fatalf("line %d wider than terminal (%d > %d): %q", i, w, uiModel.width+2, line)
		}
	}
	if testing.Verbose() {
		fmt.Println(out)
	}
}

func TestHeaderHidesCostTileWithoutProvider(t *testing.T) {
	uiModel := costTestModel(t)
	for _, node := range eksFleetNodes() {
		uiModel.Cluster().AddNode(node)
	}
	if out := ansi.Strip(uiModel.View()); strings.Contains(out, "CLUSTER COST") {
		t.Fatalf("View() shows the cost tile with no price provider\n%s", out)
	}
}

func TestFormatUSDHelpers(t *testing.T) {
	hourly := map[float64]string{
		0.29:   "$0.29/hr",
		12.345: "$12.3/hr",
		250.4:  "$250/hr",
	}
	for usd, want := range hourly {
		if got := formatHourlyUSD(usd); got != want {
			t.Errorf("formatHourlyUSD(%v) = %q, want %q", usd, got, want)
		}
	}
	monthly := map[float64]string{
		209:    "$209/mo",
		18490:  "$18.5k/mo",
		9999.4: "$9999/mo",
	}
	for usd, want := range monthly {
		if got := formatMonthlyUSD(usd); got != want {
			t.Errorf("formatMonthlyUSD(%v) = %q, want %q", usd, got, want)
		}
	}
}
