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
	"bytes"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/paginator"
	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/facette/natsort"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/lavluda/k8s-pods-viewer/pkg/text"
)

var (
	podsHelpStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262")).Render
	// white / black
	podsActiveDot = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "235", Dark: "252"}).Render("•")
	// black / white
	podsInactiveDot = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "250", Dark: "238"}).Render("•")
)

type PodsUIModel struct {
	cpuBar     progress.Model
	memBar     progress.Model
	defaultBar progress.Model
	cluster    *Cluster
	paginator  paginator.Model
	height     int
	width      int
	podSorter  func(lhs, rhs *Pod) bool
	style      *Style
	resources  []v1.ResourceName
}

func NewPodsUIModel(podSort string, style *Style) *PodsUIModel {
	pager := paginator.New()
	pager.Type = paginator.Dots
	pager.ActiveDot = podsActiveDot
	pager.InactiveDot = podsInactiveDot

	m := &PodsUIModel{
		cpuBar:     progress.New(style.gradient),
		memBar:     progress.New(progress.WithGradient("#1D4ED8", "#22D3EE")),
		defaultBar: progress.New(style.gradient),
		cluster:    NewCluster(),
		paginator:  pager,
		style:      style,
	}
	m.cpuBar.ShowPercentage = false
	m.memBar.ShowPercentage = false
	m.defaultBar.ShowPercentage = false
	m.SetResources([]string{string(v1.ResourceCPU)})
	m.podSorter = makePodSorter(podSort)
	return m
}

func (u *PodsUIModel) Cluster() *Cluster {
	return u.cluster
}

func (u *PodsUIModel) Init() tea.Cmd {
	return nil
}

func (u *PodsUIModel) View() string {
	b := strings.Builder{}
	stats := u.cluster.Stats()
	pods := u.cluster.VisiblePods()
	sort.Slice(pods, func(a, b int) bool {
		return u.podSorter(pods[a], pods[b])
	})

	ctw := text.NewColorTabWriter(&b, 0, 8, 1)
	u.writeClusterSummary(stats, pods, ctw)
	ctw.Flush()

	enPrinter := message.NewPrinter(language.English)
	enPrinter.Fprintf(&b, "%d pods (%d pending %d running %d bound)\n", stats.TotalPods,
		stats.PodsByPhase[v1.PodPending], stats.PodsByPhase[v1.PodRunning], stats.BoundPodCount)

	if len(pods) == 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "Waiting for update or no pods found...")
		fmt.Fprintln(&b, u.paginator.View())
		fmt.Fprintln(&b, podsHelpStyle("←/→ page • q: quit"))
		return u.combineWithNodeUsagePanel(b.String(), stats.Nodes, pods)
	}

	fmt.Fprintln(&b)
	u.paginator.PerPage = u.computeItemsPerPage(pods, &b)
	u.paginator.SetTotalPages(len(pods))
	if u.paginator.Page*u.paginator.PerPage > len(pods) {
		u.paginator.Page = u.paginator.TotalPages - 1
	}

	start, end := u.paginator.GetSliceBounds(len(pods))
	if start >= 0 && end >= start {
		for _, p := range pods[start:end] {
			u.writePodInfo(p, ctw)
		}
	}
	ctw.Flush()

	fmt.Fprintln(&b, u.paginator.View())
	fmt.Fprintln(&b, podsHelpStyle("←/→ page • q: quit"))
	return u.combineWithNodeUsagePanel(b.String(), stats.Nodes, pods)
}

func (u *PodsUIModel) writeClusterSummary(stats Stats, pods []*Pod, w io.Writer) {
	firstLine := true
	for _, res := range u.resources {
		allocatable := stats.AllocatableResources[res]
		used := sumUsedResource(pods, res)

		pctUsed := 0.0
		if allocatable.AsApproximateFloat64() != 0 {
			pctUsed = 100 * (used.AsApproximateFloat64() / allocatable.AsApproximateFloat64())
		}

		pctUsedStr := fmt.Sprintf("%0.1f%%", pctUsed)
		switch {
		case pctUsed > 90:
			pctUsedStr = u.style.green(pctUsedStr)
		case pctUsed > 60:
			pctUsedStr = u.style.yellow(pctUsedStr)
		default:
			pctUsedStr = u.style.red(pctUsedStr)
		}

		bar := u.progressForResource(res)
		if firstLine {
			fmt.Fprintf(w, "%d nodes\t(%10s/%s)\t%s\t%s\t%s\n",
				stats.NumNodes, formatResourceQuantity(res, used), formatResourceQuantity(res, allocatable), pctUsedStr, res, bar.ViewAs(boundPct(pctUsed/100.0)))
		} else {
			fmt.Fprintf(w, " \t%s/%s\t%s\t%s\t%s\n",
				formatResourceQuantity(res, used), formatResourceQuantity(res, allocatable), pctUsedStr, res, bar.ViewAs(boundPct(pctUsed/100.0)))
		}
		firstLine = false
	}
}

func (u *PodsUIModel) writePodInfo(p *Pod, w io.Writer) {
	usage := p.Usage()
	requested := p.Requested()
	limits := p.Limits()
	firstLine := true
	for _, res := range u.resources {
		usedRes := usage[res]
		requestedRes := requested[res]
		denominator := "-"
		nodeName := p.NodeName()
		if nodeName == "" {
			nodeName = "Pending"
		}

		pct := 0.0
		if limitRes, ok := limits[res]; ok {
			denominator = formatResourceQuantity(res, limitRes)
			if limitRes.AsApproximateFloat64() != 0 {
				pct = usedRes.AsApproximateFloat64() / limitRes.AsApproximateFloat64()
			}
		}

		bar := u.progressForResource(res)
		if firstLine {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s/%s\t%s\t%s", p.FullName(), res, bar.ViewAs(boundPct(pct)), formatResourceQuantity(res, requestedRes), denominator, p.Phase(), nodeName)
		} else {
			fmt.Fprintf(w, " \t%s\t%s\t%s/%s\t\t", res, bar.ViewAs(boundPct(pct)), formatResourceQuantity(res, requestedRes), denominator)
		}
		fmt.Fprintln(w)
		firstLine = false
	}
}

func (u *PodsUIModel) computeItemsPerPage(pods []*Pod, b *strings.Builder) int {
	var buf bytes.Buffer
	u.writePodInfo(pods[0], &buf)
	headerLines := strings.Count(b.String(), "\n") + 2
	podLines := strings.Count(buf.String(), "\n")
	if podLines == 0 {
		podLines = 1
	}
	items := ((u.height - headerLines) / podLines) - 1
	if items < 1 {
		items = 1
	}
	return items
}

type podsTickMsg time.Time

func podsTickCmd() tea.Cmd {
	return tea.Tick(1*time.Second, func(t time.Time) tea.Msg {
		return podsTickMsg(t)
	})
}

func (u *PodsUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		u.height = msg.Height
		u.width = msg.Width
		return u, podsTickCmd()
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return u, tea.Quit
		}
	case podsTickMsg:
		return u, podsTickCmd()
	}

	var cmd tea.Cmd
	u.paginator, cmd = u.paginator.Update(msg)
	return u, cmd
}

type nodeUsageRow struct {
	name        string
	cpuPct      float64
	memPct      float64
	cpuUsed     resource.Quantity
	cpuAssigned resource.Quantity
	cpuAlloc    resource.Quantity
	memUsed     resource.Quantity
	memAssigned resource.Quantity
	memAlloc    resource.Quantity
}

func (u *PodsUIModel) combineWithNodeUsagePanel(left string, nodes []*Node, pods []*Pod) string {
	if len(nodes) == 0 {
		return left
	}
	// Keep the existing single-column layout on narrow terminals.
	if u.width > 0 && u.width < 140 {
		return left
	}

	right := u.renderNodeUsagePanel(nodes, pods)
	if strings.TrimSpace(right) == "" {
		return left
	}
	return sideBySide(left, right, 3)
}

func (u *PodsUIModel) renderNodeUsagePanel(nodes []*Node, pods []*Pod) string {
	nodeUsage := map[string]v1.ResourceList{}
	nodeAssigned := map[string]v1.ResourceList{}
	for _, p := range pods {
		if p.NodeName() == "" {
			continue
		}
		if _, ok := nodeUsage[p.NodeName()]; !ok {
			nodeUsage[p.NodeName()] = v1.ResourceList{}
		}
		if _, ok := nodeAssigned[p.NodeName()]; !ok {
			nodeAssigned[p.NodeName()] = v1.ResourceList{}
		}
		for rn, q := range p.Usage() {
			existing := nodeUsage[p.NodeName()][rn]
			existing.Add(q)
			nodeUsage[p.NodeName()][rn] = existing
		}
		for rn, q := range p.Requested() {
			existing := nodeAssigned[p.NodeName()][rn]
			existing.Add(q)
			nodeAssigned[p.NodeName()][rn] = existing
		}
	}

	rows := make([]nodeUsageRow, 0, len(nodes))
	for _, n := range nodes {
		used := nodeUsage[n.Name()]
		assigned := nodeAssigned[n.Name()]
		alloc := n.Allocatable()
		cpuUsed := used[v1.ResourceCPU]
		cpuAssigned := assigned[v1.ResourceCPU]
		cpuAlloc := alloc[v1.ResourceCPU]
		memUsed := used[v1.ResourceMemory]
		memAssigned := assigned[v1.ResourceMemory]
		memAlloc := alloc[v1.ResourceMemory]
		rows = append(rows, nodeUsageRow{
			name:        n.Name(),
			cpuPct:      quantityPct(cpuUsed, cpuAlloc),
			memPct:      quantityPct(memUsed, memAlloc),
			cpuUsed:     cpuUsed,
			cpuAssigned: cpuAssigned,
			cpuAlloc:    cpuAlloc,
			memUsed:     memUsed,
			memAssigned: memAssigned,
			memAlloc:    memAlloc,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].cpuPct == rows[j].cpuPct {
			return naturalLess(rows[i].name, rows[j].name)
		}
		return rows[i].cpuPct > rows[j].cpuPct
	})

	var b strings.Builder
	fmt.Fprintln(&b, "Node CPU/Memory (used, assigned/limit)")
	for _, row := range rows {
		fmt.Fprintf(&b, "%s\n", row.name)
		fmt.Fprintf(&b, "  cpu %5.1f%%  used %s  asg/lim %s/%s\n",
			row.cpuPct*100.0, formatResourceQuantity(v1.ResourceCPU, row.cpuUsed), formatResourceQuantity(v1.ResourceCPU, row.cpuAssigned), formatResourceQuantity(v1.ResourceCPU, row.cpuAlloc))
		fmt.Fprintf(&b, "  mem %5.1f%%  used %s  asg/lim %s/%s\n",
			row.memPct*100.0, formatResourceQuantity(v1.ResourceMemory, row.memUsed), formatResourceQuantity(v1.ResourceMemory, row.memAssigned), formatResourceQuantity(v1.ResourceMemory, row.memAlloc))
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatResourceQuantity(resourceName v1.ResourceName, quantity resource.Quantity) string {
	switch resourceName {
	case v1.ResourceCPU:
		return resource.NewMilliQuantity(quantity.MilliValue(), resource.DecimalSI).String()
	default:
		return quantity.String()
	}
}

func quantityPct(used resource.Quantity, alloc resource.Quantity) float64 {
	allocFloat := alloc.AsApproximateFloat64()
	if allocFloat == 0 {
		return 0
	}
	return used.AsApproximateFloat64() / allocFloat
}

func sideBySide(left, right string, gap int) string {
	left = strings.TrimRight(left, "\n")
	right = strings.TrimRight(right, "\n")
	leftLines := strings.Split(left, "\n")
	rightLines := strings.Split(right, "\n")
	maxLines := int(math.Max(float64(len(leftLines)), float64(len(rightLines))))

	maxLeftWidth := 0
	for _, line := range leftLines {
		if w := lipgloss.Width(line); w > maxLeftWidth {
			maxLeftWidth = w
		}
	}

	var b strings.Builder
	for i := 0; i < maxLines; i++ {
		leftLine := ""
		rightLine := ""
		if i < len(leftLines) {
			leftLine = leftLines[i]
		}
		if i < len(rightLines) {
			rightLine = rightLines[i]
		}

		padding := maxLeftWidth - lipgloss.Width(leftLine) + gap
		if padding < gap {
			padding = gap
		}
		fmt.Fprintf(&b, "%s%s%s\n", leftLine, strings.Repeat(" ", padding), rightLine)
	}
	return b.String()
}

func (u *PodsUIModel) progressForResource(resourceName v1.ResourceName) progress.Model {
	switch resourceName {
	case v1.ResourceCPU:
		return u.cpuBar
	case v1.ResourceMemory:
		return u.memBar
	default:
		return u.defaultBar
	}
}

func (u *PodsUIModel) SetResources(resources []string) {
	u.resources = nil
	u.cluster.resources = nil
	for _, r := range resources {
		if r == "" {
			continue
		}
		rn := v1.ResourceName(strings.TrimSpace(r))
		u.resources = append(u.resources, rn)
		u.cluster.resources = append(u.cluster.resources, rn)
	}
	if len(u.resources) == 0 {
		u.resources = []v1.ResourceName{v1.ResourceCPU}
		u.cluster.resources = []v1.ResourceName{v1.ResourceCPU}
	}
}

func makePodSorter(podSort string) func(lhs *Pod, rhs *Pod) bool {
	sortOrder := func(b bool) bool { return b }
	if strings.HasSuffix(podSort, "=asc") {
		podSort = strings.TrimSuffix(podSort, "=asc")
	}
	if strings.HasSuffix(podSort, "=dsc") {
		sortOrder = func(b bool) bool { return !b }
		podSort = strings.TrimSuffix(podSort, "=dsc")
	}

	key := strings.TrimSpace(podSort)
	if key == "" {
		key = "cpu"
	}

	return func(lhs *Pod, rhs *Pod) bool {
		lhsVal, rhsVal := "", ""
		switch key {
		case "creation":
			if lhs.Created().Equal(rhs.Created()) {
				return sortOrder(naturalLess(lhs.FullName(), rhs.FullName()))
			}
			return sortOrder(lhs.Created().Before(rhs.Created()))
		case "namespace":
			lhsVal, rhsVal = lhs.Namespace(), rhs.Namespace()
		case "name":
			lhsVal, rhsVal = lhs.Name(), rhs.Name()
		case "node":
			lhsVal, rhsVal = lhs.NodeName(), rhs.NodeName()
		case "phase":
			lhsVal, rhsVal = string(lhs.Phase()), string(rhs.Phase())
		default:
			res := v1.ResourceName(key)
			lq := lhs.Usage()[res]
			rq := rhs.Usage()[res]
			if lq.AsApproximateFloat64() == 0 && rq.AsApproximateFloat64() == 0 {
				lq = lhs.Requested()[res]
				rq = rhs.Requested()[res]
			}
			if lq.Cmp(rq) == 0 {
				return sortOrder(naturalLess(lhs.FullName(), rhs.FullName()))
			}
			return sortOrder(lq.Cmp(rq) < 0)
		}

		if lhsVal == rhsVal {
			return sortOrder(naturalLess(lhs.FullName(), rhs.FullName()))
		}
		return sortOrder(naturalLess(lhsVal, rhsVal))
	}
}

func naturalLess(lhs, rhs string) bool {
	return natsort.Compare(lhs, rhs)
}

func sumRequestedResource(pods []*Pod, resourceName v1.ResourceName) resource.Quantity {
	total := resource.MustParse("0")
	for _, p := range pods {
		req := p.Requested()[resourceName]
		total.Add(req)
	}
	return total
}

func sumUsedResource(pods []*Pod, resourceName v1.ResourceName) resource.Quantity {
	total := resource.MustParse("0")
	for _, p := range pods {
		used := p.Usage()[resourceName]
		total.Add(used)
	}
	return total
}

func boundPct(p float64) float64 {
	if p < 0 {
		return 0
	}
	if p > 1 {
		return 1
	}
	return p
}
