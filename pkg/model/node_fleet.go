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

import "sort"

// PlatformProfile describes how one Kubernetes platform names its concepts, so
// the UI can label node metadata with provider-native wording instead of a
// lowest-common-denominator term.
type PlatformProfile struct {
	// Name is the detected platform, e.g. `EKS`.
	Name string
	// PanelTitle is the node metadata panel heading, e.g. `EKS Nodes`.
	PanelTitle string
	// PoolLabel is the provider's term for a group of nodes, e.g. `nodegroup`.
	PoolLabel string
	// Cloud reports whether the platform exposes machine metadata such as
	// instance type, capacity type, and zone.
	Cloud bool
}

// platformProfiles is ordered by specificity: earlier entries win ties when a
// cluster mixes platforms (an EKS cluster with unmanaged AWS nodes reads as EKS).
var platformProfiles = []PlatformProfile{
	{Name: "EKS", PanelTitle: "EKS Nodes", PoolLabel: "nodegroup", Cloud: true},
	{Name: "GKE", PanelTitle: "GKE Nodes", PoolLabel: "nodepool", Cloud: true},
	{Name: "AKS", PanelTitle: "AKS Nodes", PoolLabel: "agentpool", Cloud: true},
	{Name: "Karpenter", PanelTitle: "Karpenter Nodes", PoolLabel: "nodepool", Cloud: true},
	{Name: "AWS", PanelTitle: "AWS Nodes", PoolLabel: "pool", Cloud: true},
	{Name: "GCP", PanelTitle: "GCP Nodes", PoolLabel: "pool", Cloud: true},
	{Name: "Azure", PanelTitle: "Azure Nodes", PoolLabel: "pool", Cloud: true},
	{Name: "Kubernetes", PanelTitle: "Node Fleet", PoolLabel: "pool", Cloud: false},
}

// PlatformProfileFor returns the profile for a platform reported by
// NodeDetails.Platform, falling back to the generic Kubernetes profile.
func PlatformProfileFor(platform string) PlatformProfile {
	for _, profile := range platformProfiles {
		if profile.Name == platform {
			return profile
		}
	}
	generic := platformProfiles[len(platformProfiles)-1]
	if platform != "" {
		generic.Name = platform
		generic.PanelTitle = platform + " Nodes"
	}
	return generic
}

// FleetCount is one distinct metadata value and how many nodes carry it.
type FleetCount struct {
	Value string
	Count int
}

// NodeFleetEntry is the platform metadata of a single node.
type NodeFleetEntry struct {
	Name     string
	Alias    string
	Details  NodeDetails
	Pods     int
	Ready    bool
	Cordoned bool
}

// NodeFleet is the platform-shaped view of the visible nodes: one entry per
// node plus rollups of the metadata values present across the cluster.
type NodeFleet struct {
	Profile         PlatformProfile
	Entries         []NodeFleetEntry
	Pools           []FleetCount
	InstanceTypes   []FleetCount
	CapacityTypes   []FleetCount
	Zones           []FleetCount
	Regions         []FleetCount
	Images          []FleetCount
	OSImages        []FleetCount
	Architectures   []FleetCount
	KubeletVersions []FleetCount
	Runtimes        []FleetCount
}

// BuildNodeFleet collects per-node platform metadata and its cluster-wide
// rollups. The platform is detected from the nodes themselves, so the same code
// path serves EKS, GKE, AKS, and vanilla Kubernetes.
func BuildNodeFleet(nodes []*Node, aliases map[string]string) NodeFleet {
	fleet := NodeFleet{Profile: PlatformProfileFor("")}
	if len(nodes) == 0 {
		return fleet
	}

	platforms := map[string]int{}
	pools := map[string]int{}
	instanceTypes := map[string]int{}
	capacityTypes := map[string]int{}
	zones := map[string]int{}
	regions := map[string]int{}
	images := map[string]int{}
	osImages := map[string]int{}
	architectures := map[string]int{}
	kubeletVersions := map[string]int{}
	runtimes := map[string]int{}

	count := func(bucket map[string]int, value string) {
		if value == "" {
			return
		}
		bucket[value]++
	}

	for _, node := range nodes {
		details := node.Details()
		fleet.Entries = append(fleet.Entries, NodeFleetEntry{
			Name:     node.Name(),
			Alias:    nodeAlias(aliases, node.Name()),
			Details:  details,
			Pods:     node.NumPods(),
			Ready:    node.Ready(),
			Cordoned: node.Cordoned(),
		})

		count(platforms, details.Platform)
		count(pools, details.Pool)
		count(instanceTypes, details.InstanceType)
		count(capacityTypes, details.CapacityType)
		count(zones, details.Zone)
		count(regions, details.Region)
		count(images, details.ImageID)
		count(osImages, details.OSImage)
		count(architectures, nodeArchLabel(details))
		count(kubeletVersions, details.KubeletVersion)
		count(runtimes, details.ContainerRuntime)
	}

	sort.Slice(fleet.Entries, func(i, j int) bool {
		return naturalLess(fleet.Entries[i].Name, fleet.Entries[j].Name)
	})

	fleet.Profile = PlatformProfileFor(dominantPlatform(platforms))
	fleet.Pools = sortedFleetCounts(pools)
	fleet.InstanceTypes = sortedFleetCounts(instanceTypes)
	fleet.CapacityTypes = sortedFleetCounts(capacityTypes)
	fleet.Zones = sortedFleetCounts(zones)
	fleet.Regions = sortedFleetCounts(regions)
	fleet.Images = sortedFleetCounts(images)
	fleet.OSImages = sortedFleetCounts(osImages)
	fleet.Architectures = sortedFleetCounts(architectures)
	fleet.KubeletVersions = sortedFleetCounts(kubeletVersions)
	fleet.Runtimes = sortedFleetCounts(runtimes)
	return fleet
}

// dominantPlatform picks the platform most nodes report, breaking ties with the
// specificity order of platformProfiles.
func dominantPlatform(platforms map[string]int) string {
	best := ""
	bestCount := 0
	bestRank := len(platformProfiles) + 1
	for platform, nodes := range platforms {
		rank := platformRank(platform)
		if nodes > bestCount || (nodes == bestCount && rank < bestRank) {
			best, bestCount, bestRank = platform, nodes, rank
		}
	}
	return best
}

func platformRank(platform string) int {
	for i, profile := range platformProfiles {
		if profile.Name == platform {
			return i
		}
	}
	return len(platformProfiles)
}

func sortedFleetCounts(bucket map[string]int) []FleetCount {
	if len(bucket) == 0 {
		return nil
	}
	counts := make([]FleetCount, 0, len(bucket))
	for value, nodes := range bucket {
		counts = append(counts, FleetCount{Value: value, Count: nodes})
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].Count == counts[j].Count {
			return naturalLess(counts[i].Value, counts[j].Value)
		}
		return counts[i].Count > counts[j].Count
	})
	return counts
}

func nodeArchLabel(details NodeDetails) string {
	switch {
	case details.OS != "" && details.Architecture != "":
		return details.OS + "/" + details.Architecture
	case details.Architecture != "":
		return details.Architecture
	default:
		return details.OS
	}
}
