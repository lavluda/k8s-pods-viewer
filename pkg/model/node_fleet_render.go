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

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// fleetColumn is one column of the node metadata table. A column is only drawn
// when at least one node has a value for it, which is what makes the panel adapt
// to the platform: EKS nodes fill instance/capacity/zone/nodegroup, while a
// kind or bare-metal cluster falls back to os/arch and kubelet.
type fleetColumn struct {
	title    string
	maxWidth int
	value    func(NodeFleetEntry) string
}

const (
	fleetColumnGap      = 2
	fleetRollupLabelPad = 10
	fleetRollupMaxItems = 3
)

// renderNodeFleetPanel paints the platform node metadata panel. maxLines is the
// vertical budget the sidebar can spend without pushing the layout past the
// terminal height; the panel drops its rollup section and then its node rows to
// stay inside it, and renders nothing when even the header will not fit.
func (u *PodsUIModel) renderNodeFleetPanel(fleet NodeFleet, width int, maxLines int) string {
	if width <= 0 || len(fleet.Entries) == 0 {
		return ""
	}

	innerWidth := width - 4 // panel borders + left/right padding
	if innerWidth < 12 {
		return ""
	}

	columns := u.fleetColumns(fleet, innerWidth)
	if len(columns) == 0 {
		return ""
	}
	rollup := u.fleetRollupLines(fleet, innerWidth)

	// plan fits node rows into the line budget. Chrome is the top border, the
	// column header, and the bottom border, plus a divider and the rollup lines
	// when the rollup is kept. A truncated list spends one more line on the
	// "+N more nodes" notice so the cap is never silent.
	plan := func(withRollup bool) (rows int, hidden int, ok bool) {
		chrome := 3
		if withRollup {
			chrome += 1 + len(rollup)
		}
		available := maxLines - chrome
		if available < 1 {
			return 0, 0, false
		}
		if len(fleet.Entries) <= available {
			return len(fleet.Entries), 0, true
		}
		if available < 2 {
			return 0, 0, false
		}
		return available - 1, len(fleet.Entries) - (available - 1), true
	}

	rows, hidden, ok := plan(len(rollup) > 0)
	if len(rollup) > 0 && (!ok || (hidden > 0 && rows < 3)) {
		// Node rows carry more information than the rollup, so give up the
		// rollup before shrinking the list further.
		if planRows, planHidden, planOK := plan(false); planOK {
			rows, hidden, ok = planRows, planHidden, planOK
			rollup = nil
		}
	}
	if !ok {
		return ""
	}

	var inner strings.Builder
	inner.WriteString(renderFleetHeaderRow(columns))
	for _, entry := range fleet.Entries[:rows] {
		inner.WriteString("\n")
		inner.WriteString(u.renderFleetRow(entry, columns))
	}
	if hidden > 0 {
		fmt.Fprintf(&inner, "\n%s", lipgloss.NewStyle().Foreground(podsSurfaceDim).
			Render(fmt.Sprintf("+%d more nodes", hidden)))
	}
	if len(rollup) > 0 {
		inner.WriteString("\n")
		inner.WriteString(lipgloss.NewStyle().Foreground(podsPanelBorder).Render(strings.Repeat("─", innerWidth)))
		for _, line := range rollup {
			inner.WriteString("\n")
			inner.WriteString(line)
		}
	}

	right := fmt.Sprintf("%d nodes", len(fleet.Entries))
	return renderPanel(width, fleet.Profile.PanelTitle, right, inner.String(), podsPanelBorder)
}

// fleetColumns picks the columns that have data and fit the panel width.
func (u *PodsUIModel) fleetColumns(fleet NodeFleet, innerWidth int) []fleetColumn {
	candidates := []fleetColumn{
		{title: "NODE", maxWidth: 20, value: func(e NodeFleetEntry) string { return e.Alias }},
	}
	if u.showDetails {
		// Detailed mode trades the trailing os/kubelet columns (still summarized
		// in the rollup) for the machine identity operators need for cloud CLIs.
		candidates = append(candidates,
			fleetColumn{title: "MACHINE", maxWidth: 21, value: func(e NodeFleetEntry) string { return e.Details.InstanceID }},
		)
	}
	candidates = append(candidates,
		fleetColumn{title: "INSTANCE", maxWidth: 16, value: func(e NodeFleetEntry) string { return e.Details.InstanceType }},
		fleetColumn{title: "CAPACITY", maxWidth: 10, value: func(e NodeFleetEntry) string { return e.Details.CapacityType }},
		fleetColumn{title: "ZONE", maxWidth: 16, value: func(e NodeFleetEntry) string {
			return firstNonEmpty(e.Details.Zone, e.Details.Region)
		}},
		fleetColumn{title: strings.ToUpper(fleet.Profile.PoolLabel), maxWidth: 18, value: func(e NodeFleetEntry) string {
			return e.Details.Pool
		}},
		fleetColumn{title: "ARCH", maxWidth: 14, value: func(e NodeFleetEntry) string { return nodeArchLabel(e.Details) }},
		fleetColumn{title: "KUBELET", maxWidth: 22, value: func(e NodeFleetEntry) string { return e.Details.KubeletVersion }},
	)

	columns := make([]fleetColumn, 0, len(candidates))
	used := 0
	for _, column := range candidates {
		contentWidth := len(column.title)
		populated := false
		for _, entry := range fleet.Entries {
			value := column.value(entry)
			if value == "" {
				continue
			}
			populated = true
			if len(value) > contentWidth {
				contentWidth = len(value)
			}
		}
		if !populated {
			continue
		}
		if contentWidth > column.maxWidth {
			contentWidth = column.maxWidth
		}

		next := used + contentWidth
		if len(columns) > 0 {
			next += fleetColumnGap
		}
		if next > innerWidth {
			// Later candidates are narrower only by chance; stop so column
			// priority (not width luck) decides what survives.
			break
		}
		column.maxWidth = contentWidth
		columns = append(columns, column)
		used = next
	}
	return columns
}

func renderFleetHeaderRow(columns []fleetColumn) string {
	cells := make([]string, 0, len(columns))
	for _, column := range columns {
		cells = append(cells, padFleetCell(truncateRunes(column.title, column.maxWidth), column.maxWidth))
	}
	return lipgloss.NewStyle().Foreground(podsSurfaceDim).Render(strings.Join(cells, strings.Repeat(" ", fleetColumnGap)))
}

func (u *PodsUIModel) renderFleetRow(entry NodeFleetEntry, columns []fleetColumn) string {
	cells := make([]string, 0, len(columns))
	for i, column := range columns {
		text := padFleetCell(truncateRunes(column.value(entry), column.maxWidth), column.maxWidth)
		style := lipgloss.NewStyle().Foreground(podsSurfaceMuted)
		if i == 0 {
			style = lipgloss.NewStyle().Foreground(podsSurfaceText).Bold(true)
			switch {
			case !entry.Ready:
				style = lipgloss.NewStyle().Foreground(lipgloss.Color(u.style.redHex)).Bold(true)
			case entry.Cordoned:
				style = lipgloss.NewStyle().Foreground(lipgloss.Color(u.style.yellowHex)).Bold(true)
			}
		} else if column.title == "CAPACITY" && strings.HasPrefix(column.value(entry), "spot") {
			style = lipgloss.NewStyle().Foreground(lipgloss.Color(u.style.yellowHex))
		}
		cells = append(cells, style.Render(text))
	}
	return strings.Join(cells, strings.Repeat(" ", fleetColumnGap))
}

// fleetRollupLines summarizes the fleet for the values that are worth reading
// as a distribution rather than per node.
func (u *PodsUIModel) fleetRollupLines(fleet NodeFleet, innerWidth int) []string {
	type rollup struct {
		label  string
		counts []FleetCount
	}
	rollups := []rollup{
		{label: fleet.Profile.PoolLabel, counts: fleet.Pools},
		{label: "capacity", counts: fleet.CapacityTypes},
		{label: "instance", counts: fleet.InstanceTypes},
		{label: "zone", counts: fleet.Zones},
		{label: "kubelet", counts: fleet.KubeletVersions},
	}
	if u.showDetails {
		rollups = append(rollups,
			rollup{label: "region", counts: fleet.Regions},
			rollup{label: "arch", counts: fleet.Architectures},
			rollup{label: "image", counts: fleet.Images},
			rollup{label: "os", counts: fleet.OSImages},
			rollup{label: "runtime", counts: fleet.Runtimes},
		)
	}

	lines := make([]string, 0, len(rollups))
	for _, r := range rollups {
		if len(r.counts) == 0 {
			continue
		}
		label := lipgloss.NewStyle().Foreground(u.style.Accent()).Bold(true).
			Render(fmt.Sprintf("%-*s", fleetRollupLabelPad, strings.ToUpper(truncateRunes(r.label, fleetRollupLabelPad))))
		valueWidth := innerWidth - ansi.StringWidth(label) - 1
		if valueWidth < 8 {
			valueWidth = 8
		}
		value := lipgloss.NewStyle().Foreground(podsSurfaceMuted).
			Render(truncateRunes(formatFleetCounts(r.counts, fleetRollupMaxItems), valueWidth))
		lines = append(lines, label+" "+value)
	}
	return lines
}

// formatFleetCounts renders a distribution as `value n · value n`, dropping the
// count when a single value covers the whole fleet.
func formatFleetCounts(counts []FleetCount, maxItems int) string {
	if len(counts) == 0 {
		return ""
	}
	if len(counts) == 1 {
		return counts[0].Value
	}
	parts := make([]string, 0, maxItems+1)
	for i, count := range counts {
		if i == maxItems {
			parts = append(parts, fmt.Sprintf("+%d more", len(counts)-maxItems))
			break
		}
		parts = append(parts, fmt.Sprintf("%s %d", count.Value, count.Count))
	}
	return strings.Join(parts, " · ")
}

func padFleetCell(text string, width int) string {
	padding := width - ansi.StringWidth(text)
	if padding <= 0 {
		return text
	}
	return text + strings.Repeat(" ", padding)
}
