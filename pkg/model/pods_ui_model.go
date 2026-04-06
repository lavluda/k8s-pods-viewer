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
	"io"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/paginator"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/facette/natsort"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

var (
	podsHelpStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#71717A")).Render
	podsMutedStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#A1A1AA")).Render
	podsGroupStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E4E4E7")).Render
	podsAliasStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#94A3B8")).Render
	podsHeaderStyle     = lipgloss.NewStyle().Bold(true).Render
	podsBarEmptyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#3F3F46")).Render
	podsBarMarkerStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#71717A")).Render
	podsCPUStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F97316")).Render
	podsMemoryStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#38BDF8")).Render
	podsDefaultResStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#22C55E")).Render
	// white / black
	podsActiveDot = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "235", Dark: "252"}).Render("•")
	// black / white
	podsInactiveDot = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "250", Dark: "238"}).Render("•")
)

type groupMode string

const (
	groupModeNamespace groupMode = "namespace"
	groupModeWorkload  groupMode = "workload"
	groupModeFlat      groupMode = "flat"
)

type podGroup struct {
	key        string
	label      string
	totalPods  int
	showHeader bool
	pods       []*Pod
}

type podUsageSnapshot struct {
	usedLabel      string
	requestedLabel string
	baselineLabel  string
	pct            float64
	hasBaseline    bool
}

type nodeUsageRow struct {
	name        string
	alias       string
	cpuPct      float64
	memPct      float64
	cpuUsed     resource.Quantity
	cpuAssigned resource.Quantity
	cpuAlloc    resource.Quantity
	memUsed     resource.Quantity
	memAssigned resource.Quantity
	memAlloc    resource.Quantity
}

type PodsUIModel struct {
	cluster     *Cluster
	paginator   paginator.Model
	filterInput textinput.Model
	height      int
	width       int
	podSorter   func(lhs, rhs *Pod) bool
	podSort     string
	style       *Style
	resources   []v1.ResourceName
	contextName string
	namespace   string
	groupMode   groupMode
	showDetails bool
	filtering   bool
	filterQuery string
	statusMu    sync.RWMutex
	statusLine  string
	statusUntil time.Time
}

func NewPodsUIModel(podSort string, style *Style) *PodsUIModel {
	pager := paginator.New()
	pager.Type = paginator.Dots
	pager.ActiveDot = podsActiveDot
	pager.InactiveDot = podsInactiveDot
	pager.PerPage = 1

	filterInput := textinput.New()
	filterInput.Prompt = "/ "
	filterInput.Placeholder = "search pods, namespaces, workloads, nodes"
	filterInput.CharLimit = 128

	m := &PodsUIModel{
		cluster:     NewCluster(),
		paginator:   pager,
		filterInput: filterInput,
		style:       style,
		groupMode:   groupModeNamespace,
	}
	m.SetResources([]string{string(v1.ResourceCPU)})
	m.setPodSorter(podSort)
	return m
}

func (u *PodsUIModel) Cluster() *Cluster {
	return u.cluster
}

func (u *PodsUIModel) SetContextName(name string) {
	u.contextName = strings.TrimSpace(name)
}

func (u *PodsUIModel) SetNamespace(namespace string) {
	u.namespace = strings.TrimSpace(namespace)
}

func (u *PodsUIModel) Init() tea.Cmd {
	return nil
}

func (u *PodsUIModel) View() string {
	stats := u.cluster.Stats()
	visiblePods := u.cluster.VisiblePods()
	sort.Slice(visiblePods, func(a, b int) bool {
		return u.podSorter(visiblePods[a], visiblePods[b])
	})

	filteredPods := u.filterPods(visiblePods)
	nodeAliases := buildNodeAliases(stats.Nodes, visiblePods)

	var b strings.Builder
	u.writeContextBar(&b, visiblePods, filteredPods)
	u.writeClusterSummary(&b, stats, visiblePods)
	fmt.Fprintln(&b)

	if len(filteredPods) == 0 {
		if strings.TrimSpace(u.filterQuery) != "" {
			fmt.Fprintf(&b, "No pods match %q.\n", u.filterQuery)
		} else {
			fmt.Fprintln(&b, "Waiting for update or no pods found...")
		}
	} else {
		pages := u.paginateGroups(u.groupPods(filteredPods))
		u.paginator.PerPage = 1
		u.paginator.SetTotalPages(maxInt(1, len(pages)))
		if u.paginator.Page >= u.paginator.TotalPages {
			u.paginator.Page = u.paginator.TotalPages - 1
		}
		if len(pages) > 0 && u.paginator.Page >= 0 && u.paginator.Page < len(pages) {
			u.renderPage(&b, pages[u.paginator.Page], nodeAliases)
		}
	}

	fmt.Fprintln(&b, u.paginator.View())
	u.writeFooter(&b)
	return u.combineWithNodeUsagePanel(b.String(), stats.Nodes, visiblePods, nodeAliases)
}

func (u *PodsUIModel) SetTransientStatus(status string, duration time.Duration) {
	if strings.TrimSpace(status) == "" {
		u.ClearTransientStatus()
		return
	}
	if duration <= 0 {
		duration = 5 * time.Second
	}

	u.statusMu.Lock()
	u.statusLine = strings.TrimSpace(status)
	u.statusUntil = time.Now().Add(duration)
	u.statusMu.Unlock()
}

func (u *PodsUIModel) ClearTransientStatus() {
	u.statusMu.Lock()
	u.statusLine = ""
	u.statusUntil = time.Time{}
	u.statusMu.Unlock()
}

func (u *PodsUIModel) writeStatusLine(w io.Writer) {
	if status := u.currentStatus(); status != "" {
		fmt.Fprintln(w, u.style.yellow(status))
	}
}

func (u *PodsUIModel) currentStatus() string {
	u.statusMu.RLock()
	status := u.statusLine
	until := u.statusUntil
	u.statusMu.RUnlock()

	if status == "" {
		return ""
	}
	if !until.IsZero() && time.Now().After(until) {
		u.statusMu.Lock()
		if u.statusLine == status && u.statusUntil.Equal(until) && time.Now().After(u.statusUntil) {
			u.statusLine = ""
			u.statusUntil = time.Time{}
		}
		u.statusMu.Unlock()
		return ""
	}
	return status
}

func (u *PodsUIModel) writeContextBar(w io.Writer, visiblePods []*Pod, filteredPods []*Pod) {
	namespace := u.namespace
	if namespace == "" {
		namespace = "all"
	}
	contextName := u.contextName
	if contextName == "" {
		contextName = "current"
	}

	unhealthy := countUnhealthyPods(visiblePods)
	parts := []string{
		fmt.Sprintf("Ctx: %s", contextName),
		fmt.Sprintf("NS: %s", namespace),
		fmt.Sprintf("Pods: %d", len(visiblePods)),
		fmt.Sprintf("Unhealthy: %d", unhealthy),
		fmt.Sprintf("Sort: %s", u.sortLabel()),
		fmt.Sprintf("Group: %s", u.groupModeLabel()),
		fmt.Sprintf("Time: %s", time.Now().Format("15:04:05")),
	}
	if len(filteredPods) != len(visiblePods) {
		parts = append(parts, fmt.Sprintf("Shown: %d", len(filteredPods)))
	}

	fmt.Fprintln(w, podsHeaderStyle(strings.Join(parts, " | ")))
}

func (u *PodsUIModel) writeClusterSummary(w io.Writer, stats Stats, pods []*Pod) {
	for _, res := range u.resources {
		allocatable := stats.AllocatableResources[res]
		used := sumUsedResource(pods, res)
		pctUsed := quantityPct(used, allocatable)
		avgPct := averagePodUsagePct(pods, res)

		fmt.Fprintf(w, "%s %s  %s/%s  avg %s\n",
			u.resourceLabel(res),
			u.renderUsageBar(res, pctUsed, u.summaryBarWidth()),
			formatResourceQuantity(res, used),
			formatResourceQuantity(res, allocatable),
			u.colorizePct(avgPct, resourceSeverity(avgPct)),
		)
	}
}

func (u *PodsUIModel) renderPage(w io.Writer, page []podGroup, nodeAliases map[string]string) {
	for groupIndex, group := range page {
		if group.showHeader {
			fmt.Fprintf(w, "%s %s %s\n",
				podsGroupStyle(group.label),
				podsMutedStyle("("),
				podsMutedStyle(fmt.Sprintf("%d pods)", group.totalPods)),
			)
		}
		for _, pod := range group.pods {
			u.renderPod(w, pod, nodeAliases)
		}
		if groupIndex < len(page)-1 {
			fmt.Fprintln(w)
		}
	}
}

func (u *PodsUIModel) renderPod(w io.Writer, pod *Pod, nodeAliases map[string]string) {
	health := pod.Health()
	name := pod.Name()
	if u.groupMode == groupModeFlat {
		name = pod.FullName()
	}
	name = u.renderPodName(name, pod)

	nodeSummary := "pending"
	if pod.NodeName() != "" {
		nodeSummary = podsAliasStyle(nodeAlias(nodeAliases, pod.NodeName()))
		if u.showDetails {
			nodeSummary = fmt.Sprintf("%s (%s)", nodeSummary, pod.NodeName())
		}
	}

	extras := []string{
		u.renderHealth(health),
		nodeSummary,
	}
	if restarts := pod.Restarts(); restarts > 0 {
		extras = append(extras, u.style.yellow(fmt.Sprintf("%dr", restarts)))
	}

	fmt.Fprintf(w, "  %s  %s\n", name, strings.Join(extras, "  "))
	usage := pod.Usage()
	requested := pod.Requested()
	limits := pod.Limits()
	for _, res := range u.resources {
		summary := summarizePodUsage(res, usage[res], requested[res], limits)
		fmt.Fprintf(w, "    %s %s  %s/%s",
			u.resourceLabel(res),
			u.renderUsageBar(res, summary.pct, u.resourceBarWidth()),
			formatResourceQuantity(res, usage[res]),
			summary.baselineLabel,
		)
		if u.showDetails {
			fmt.Fprintf(w, "  req/lim %s/%s", summary.requestedLabel, limitLabelForResource(res, requested[res], limits))
		}
		fmt.Fprintln(w)
	}
}

func (u *PodsUIModel) writeFooter(w io.Writer) {
	if u.filtering {
		fmt.Fprintf(w, "Filter: %s\n", u.filterInput.View())
	} else if strings.TrimSpace(u.filterQuery) != "" {
		fmt.Fprintf(w, "Filter: %s\n", podsAliasStyle(u.filterQuery))
	}
	u.writeStatusLine(w)
	fmt.Fprintln(w, podsHelpStyle("←/→ page • c cpu • m mem • s status • g group • i info • / filter • q quit"))
}

func (u *PodsUIModel) groupPods(pods []*Pod) []podGroup {
	if len(pods) == 0 {
		return nil
	}
	if u.groupMode == groupModeFlat {
		return []podGroup{{
			key:        "all",
			showHeader: false,
			totalPods:  len(pods),
			pods:       pods,
		}}
	}

	groups := map[string]*podGroup{}
	order := []string{}
	for _, pod := range pods {
		key, label := u.groupKeyForPod(pod)
		group, ok := groups[key]
		if !ok {
			group = &podGroup{
				key:        key,
				label:      label,
				showHeader: true,
			}
			groups[key] = group
			order = append(order, key)
		}
		group.totalPods++
		group.pods = append(group.pods, pod)
	}

	result := make([]podGroup, 0, len(order))
	for _, key := range order {
		result = append(result, *groups[key])
	}
	return result
}

func (u *PodsUIModel) groupKeyForPod(pod *Pod) (key string, label string) {
	switch u.groupMode {
	case groupModeWorkload:
		kind, workload := pod.Workload()
		return fmt.Sprintf("%s|%s|%s", pod.Namespace(), kind, workload), fmt.Sprintf("%s / %s %s", pod.Namespace(), strings.ToLower(kind), workload)
	default:
		return pod.Namespace(), pod.Namespace()
	}
}

func (u *PodsUIModel) paginateGroups(groups []podGroup) [][]podGroup {
	if len(groups) == 0 {
		return nil
	}
	budget := u.availablePodLines()
	if budget <= 0 {
		return [][]podGroup{groups}
	}

	linesPerPod := u.linesPerPod()
	pages := [][]podGroup{}
	page := []podGroup{}
	remaining := budget

	flushPage := func() {
		if len(page) == 0 {
			return
		}
		pages = append(pages, page)
		page = nil
		remaining = budget
	}

	for _, group := range groups {
		index := 0
		for index < len(group.pods) {
			headerCost := 0
			if group.showHeader {
				headerCost = 1
			}

			required := linesPerPod + headerCost
			if remaining < required && len(page) > 0 {
				flushPage()
			}
			if remaining < required {
				remaining = required
			}

			fit := (remaining - headerCost) / linesPerPod
			if fit < 1 {
				fit = 1
			}
			if fit > len(group.pods)-index {
				fit = len(group.pods) - index
			}

			page = append(page, podGroup{
				key:        group.key,
				label:      group.label,
				totalPods:  group.totalPods,
				showHeader: group.showHeader,
				pods:       group.pods[index : index+fit],
			})
			remaining -= headerCost + fit*linesPerPod
			index += fit

			if remaining < linesPerPod && index < len(group.pods) {
				flushPage()
			}
		}
	}
	flushPage()
	return pages
}

func (u *PodsUIModel) filterPods(pods []*Pod) []*Pod {
	query := strings.ToLower(strings.TrimSpace(u.filterQuery))
	if query == "" {
		return pods
	}

	filtered := make([]*Pod, 0, len(pods))
	for _, pod := range pods {
		if podMatchesFilter(pod, query) {
			filtered = append(filtered, pod)
		}
	}
	return filtered
}

func podMatchesFilter(pod *Pod, query string) bool {
	kind, workload := pod.Workload()
	fields := []string{
		pod.FullName(),
		pod.Namespace(),
		pod.Name(),
		pod.NodeName(),
		kind,
		workload,
		pod.Health().Label,
		string(pod.Phase()),
	}
	return strings.Contains(strings.ToLower(strings.Join(fields, " ")), query)
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
		u.filterInput.Width = maxInt(24, minInt(72, msg.Width/2))
		return u, podsTickCmd()
	case tea.KeyMsg:
		if u.filtering {
			return u.handleFilteringKey(msg)
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return u, tea.Quit
		case "esc":
			if strings.TrimSpace(u.filterQuery) != "" {
				u.filterQuery = ""
				u.filterInput.SetValue("")
				u.paginator.Page = 0
				u.SetTransientStatus("Filter cleared.", 2*time.Second)
				return u, nil
			}
			return u, tea.Quit
		case "/":
			u.filtering = true
			u.filterInput.Focus()
			u.filterInput.SetValue(u.filterQuery)
			u.filterInput.CursorEnd()
			return u, textinput.Blink
		case "c":
			u.setPodSorter("cpu=dsc")
			u.paginator.Page = 0
			u.SetTransientStatus("Sorted by CPU.", 2*time.Second)
			return u, nil
		case "m":
			u.setPodSorter("memory=dsc")
			u.paginator.Page = 0
			u.SetTransientStatus("Sorted by memory.", 2*time.Second)
			return u, nil
		case "s":
			u.setPodSorter("status=dsc")
			u.paginator.Page = 0
			u.SetTransientStatus("Sorted by status.", 2*time.Second)
			return u, nil
		case "g":
			u.groupMode = u.groupMode.next()
			u.paginator.Page = 0
			u.SetTransientStatus(fmt.Sprintf("Grouping by %s.", u.groupModeLabel()), 2*time.Second)
			return u, nil
		case "i":
			u.showDetails = !u.showDetails
			u.paginator.Page = 0
			if u.showDetails {
				u.SetTransientStatus("Detailed info enabled.", 2*time.Second)
			} else {
				u.SetTransientStatus("Compact mode enabled.", 2*time.Second)
			}
			return u, nil
		}
	case podsTickMsg:
		return u, podsTickCmd()
	}

	var cmd tea.Cmd
	u.paginator, cmd = u.paginator.Update(msg)
	return u, cmd
}

func (u *PodsUIModel) handleFilteringKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return u, tea.Quit
	case "esc", "enter":
		u.filtering = false
		u.filterInput.Blur()
		u.filterQuery = strings.TrimSpace(u.filterInput.Value())
		u.paginator.Page = 0
		return u, nil
	}

	var cmd tea.Cmd
	u.filterInput, cmd = u.filterInput.Update(msg)
	u.filterQuery = strings.TrimSpace(u.filterInput.Value())
	u.paginator.Page = 0
	return u, cmd
}

func (u *PodsUIModel) combineWithNodeUsagePanel(left string, nodes []*Node, pods []*Pod, nodeAliases map[string]string) string {
	if len(nodes) == 0 {
		return left
	}
	if u.width > 0 && u.width < 150 {
		return left
	}

	right := u.renderNodeUsagePanel(nodes, pods, nodeAliases)
	if strings.TrimSpace(right) == "" {
		return left
	}
	return sideBySide(left, right, 3)
}

func (u *PodsUIModel) renderNodeUsagePanel(nodes []*Node, pods []*Pod, nodeAliases map[string]string) string {
	nodeUsage := map[string]v1.ResourceList{}
	nodeAssigned := map[string]v1.ResourceList{}
	for _, pod := range pods {
		if pod.NodeName() == "" {
			continue
		}
		if _, ok := nodeUsage[pod.NodeName()]; !ok {
			nodeUsage[pod.NodeName()] = v1.ResourceList{}
		}
		if _, ok := nodeAssigned[pod.NodeName()]; !ok {
			nodeAssigned[pod.NodeName()] = v1.ResourceList{}
		}
		for rn, q := range pod.Usage() {
			existing := nodeUsage[pod.NodeName()][rn]
			existing.Add(q)
			nodeUsage[pod.NodeName()][rn] = existing
		}
		for rn, q := range pod.Requested() {
			existing := nodeAssigned[pod.NodeName()][rn]
			existing.Add(q)
			nodeAssigned[pod.NodeName()][rn] = existing
		}
	}

	rows := make([]nodeUsageRow, 0, len(nodes))
	for _, node := range nodes {
		used := nodeUsage[node.Name()]
		assigned := nodeAssigned[node.Name()]
		alloc := node.Allocatable()
		rows = append(rows, nodeUsageRow{
			name:        node.Name(),
			alias:       nodeAlias(nodeAliases, node.Name()),
			cpuPct:      quantityPct(used[v1.ResourceCPU], alloc[v1.ResourceCPU]),
			memPct:      quantityPct(used[v1.ResourceMemory], alloc[v1.ResourceMemory]),
			cpuUsed:     used[v1.ResourceCPU],
			cpuAssigned: assigned[v1.ResourceCPU],
			cpuAlloc:    alloc[v1.ResourceCPU],
			memUsed:     used[v1.ResourceMemory],
			memAssigned: assigned[v1.ResourceMemory],
			memAlloc:    alloc[v1.ResourceMemory],
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].cpuPct == rows[j].cpuPct {
			return naturalLess(rows[i].name, rows[j].name)
		}
		return rows[i].cpuPct > rows[j].cpuPct
	})

	var b strings.Builder
	fmt.Fprintln(&b, podsHeaderStyle("Node Pressure"))
	for _, row := range rows {
		name := row.alias
		if u.showDetails {
			name = fmt.Sprintf("%s (%s)", row.alias, row.name)
		}
		fmt.Fprintln(&b, name)
		fmt.Fprintf(&b, "  %s %s  %s/%s  asg %s/%s\n",
			u.resourceLabel(v1.ResourceCPU),
			u.renderUsageBar(v1.ResourceCPU, row.cpuPct, u.nodeBarWidth()),
			formatResourceQuantity(v1.ResourceCPU, row.cpuUsed),
			formatResourceQuantity(v1.ResourceCPU, row.cpuAlloc),
			formatResourceQuantity(v1.ResourceCPU, row.cpuAssigned),
			formatResourceQuantity(v1.ResourceCPU, row.cpuAlloc),
		)
		fmt.Fprintf(&b, "  %s %s  %s/%s  asg %s/%s\n",
			u.resourceLabel(v1.ResourceMemory),
			u.renderUsageBar(v1.ResourceMemory, row.memPct, u.nodeBarWidth()),
			formatResourceQuantity(v1.ResourceMemory, row.memUsed),
			formatResourceQuantity(v1.ResourceMemory, row.memAlloc),
			formatResourceQuantity(v1.ResourceMemory, row.memAssigned),
			formatResourceQuantity(v1.ResourceMemory, row.memAlloc),
		)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (u *PodsUIModel) renderPodName(name string, pod *Pod) string {
	severity := podAttentionSeverity(pod, u.resources)
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(u.style.SeverityColor(severity)).
		Render(name)
}

func (u *PodsUIModel) renderHealth(health PodHealth) string {
	style := lipgloss.NewStyle().Foreground(u.style.SeverityColor(health.Severity))
	return style.Render(fmt.Sprintf("%s %s", health.Icon, health.Label))
}

func (u *PodsUIModel) resourceLabel(resourceName v1.ResourceName) string {
	switch resourceName {
	case v1.ResourceCPU:
		return podsCPUStyle("CPU")
	case v1.ResourceMemory:
		return podsMemoryStyle("MEM")
	default:
		return podsDefaultResStyle(strings.ToUpper(string(resourceName)))
	}
}

func (u *PodsUIModel) renderUsageBar(resourceName v1.ResourceName, pct float64, width int) string {
	if width < 10 {
		width = 10
	}
	fillStyle := u.resourceBarFill(resourceName)
	labelStyle := lipgloss.NewStyle().Foreground(u.style.SeverityColor(resourceSeverity(pct))).Bold(true).Render
	label := fmt.Sprintf("%.0f%%", pct*100)
	if pct*100 >= 1000 {
		label = "999%+"
	}

	boundedPct := boundPct(pct)
	filled := int(math.Round(boundedPct * float64(width)))
	warnAt := int(math.Round(0.7 * float64(width)))
	criticalAt := int(math.Round(0.9 * float64(width)))

	cells := make([]string, width)
	for i := 0; i < width; i++ {
		switch {
		case i < filled:
			cells[i] = fillStyle("█")
		case i == warnAt-1 || i == criticalAt-1:
			cells[i] = podsBarMarkerStyle("│")
		default:
			cells[i] = podsBarEmptyStyle("░")
		}
	}

	if len(label) < width {
		start := (width - len(label)) / 2
		for offset, r := range label {
			index := start + offset
			if index >= 0 && index < width {
				cells[index] = labelStyle(string(r))
			}
		}
	}

	return "[" + strings.Join(cells, "") + "]"
}

func (u *PodsUIModel) resourceBarFill(resourceName v1.ResourceName) func(...string) string {
	switch resourceName {
	case v1.ResourceCPU:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#FB923C")).Render
	case v1.ResourceMemory:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#38BDF8")).Render
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#22C55E")).Render
	}
}

func (u *PodsUIModel) colorizePct(pct float64, severity PodHealthSeverity) string {
	return lipgloss.NewStyle().Foreground(u.style.SeverityColor(severity)).Bold(true).Render(fmt.Sprintf("%.0f%%", pct*100))
}

func (u *PodsUIModel) summaryBarWidth() int {
	switch {
	case u.width >= 190:
		return 20
	case u.width >= 150:
		return 16
	default:
		return 12
	}
}

func (u *PodsUIModel) resourceBarWidth() int {
	switch {
	case u.width >= 190:
		return 18
	case u.width >= 150:
		return 16
	default:
		return 12
	}
}

func (u *PodsUIModel) nodeBarWidth() int {
	switch {
	case u.width >= 190:
		return 16
	case u.width >= 150:
		return 14
	default:
		return 12
	}
}

func (u *PodsUIModel) linesPerPod() int {
	return 1 + len(u.resources)
}

func (u *PodsUIModel) availablePodLines() int {
	if u.height <= 0 {
		return 0
	}

	footerLines := 2
	if strings.TrimSpace(u.filterQuery) != "" || u.filtering {
		footerLines++
	}
	if u.currentStatus() != "" {
		footerLines++
	}

	headerLines := 1 + len(u.resources) + 1
	lines := u.height - headerLines - footerLines
	if lines < u.linesPerPod() {
		return u.linesPerPod()
	}
	return lines
}

func (u *PodsUIModel) setPodSorter(podSort string) {
	u.podSort = strings.TrimSpace(podSort)
	if u.podSort == "" {
		u.podSort = "cpu=dsc"
	}
	u.podSorter = makePodSorter(u.podSort)
}

func (u *PodsUIModel) sortLabel() string {
	label := strings.TrimSuffix(strings.TrimSuffix(u.podSort, "=dsc"), "=asc")
	if label == "status" {
		return "status"
	}
	if label == "" {
		return "cpu"
	}
	return label
}

func (u *PodsUIModel) groupModeLabel() string {
	switch u.groupMode {
	case groupModeWorkload:
		return "workload"
	case groupModeFlat:
		return "flat"
	default:
		return "namespace"
	}
}

func (m groupMode) next() groupMode {
	switch m {
	case groupModeNamespace:
		return groupModeWorkload
	case groupModeWorkload:
		return groupModeFlat
	default:
		return groupModeNamespace
	}
}

func buildNodeAliases(nodes []*Node, pods []*Pod) map[string]string {
	seen := map[string]struct{}{}
	names := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if _, ok := seen[node.Name()]; ok {
			continue
		}
		seen[node.Name()] = struct{}{}
		names = append(names, node.Name())
	}
	for _, pod := range pods {
		if pod.NodeName() == "" {
			continue
		}
		if _, ok := seen[pod.NodeName()]; ok {
			continue
		}
		seen[pod.NodeName()] = struct{}{}
		names = append(names, pod.NodeName())
	}

	sort.Slice(names, func(i, j int) bool {
		return naturalLess(names[i], names[j])
	})

	aliases := map[string]string{}
	for index, name := range names {
		aliases[name] = fmt.Sprintf("node-%d", index+1)
	}
	return aliases
}

func nodeAlias(aliases map[string]string, name string) string {
	if alias, ok := aliases[name]; ok {
		return alias
	}
	return name
}

func podAttentionSeverity(pod *Pod, resources []v1.ResourceName) PodHealthSeverity {
	severity := pod.Health().Severity
	usage := pod.Usage()
	requested := pod.Requested()
	limits := pod.Limits()
	for _, resourceName := range resources {
		resourceSeverity := resourceSeverity(summarizePodUsage(resourceName, usage[resourceName], requested[resourceName], limits).pct)
		if resourceSeverity > severity {
			severity = resourceSeverity
		}
	}
	return severity
}

func countUnhealthyPods(pods []*Pod) int {
	count := 0
	for _, pod := range pods {
		if pod.Health().Severity > PodHealthHealthy {
			count++
		}
	}
	return count
}

func averagePodUsagePct(pods []*Pod, resourceName v1.ResourceName) float64 {
	if len(pods) == 0 {
		return 0
	}

	total := 0.0
	count := 0
	for _, pod := range pods {
		summary := summarizePodUsage(resourceName, pod.Usage()[resourceName], pod.Requested()[resourceName], pod.Limits())
		if !summary.hasBaseline {
			continue
		}
		total += summary.pct
		count++
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func summarizePodUsage(resourceName v1.ResourceName, used resource.Quantity, requested resource.Quantity, limits v1.ResourceList) podUsageSnapshot {
	summary := podUsageSnapshot{
		usedLabel:      formatResourceQuantity(resourceName, used),
		requestedLabel: formatResourceQuantity(resourceName, requested),
		baselineLabel:  "-",
	}

	scale := resource.Quantity{}
	if limit, ok := limits[resourceName]; ok {
		summary.baselineLabel = formatResourceQuantity(resourceName, limit)
		scale = limit
		summary.hasBaseline = true
	} else if requested.AsApproximateFloat64() != 0 {
		summary.baselineLabel = formatResourceQuantity(resourceName, requested)
		scale = requested
		summary.hasBaseline = true
	}

	if scale.AsApproximateFloat64() != 0 {
		summary.pct = used.AsApproximateFloat64() / scale.AsApproximateFloat64()
	}
	return summary
}

func limitLabelForResource(resourceName v1.ResourceName, requested resource.Quantity, limits v1.ResourceList) string {
	if limit, ok := limits[resourceName]; ok {
		return formatResourceQuantity(resourceName, limit)
	}
	if requested.AsApproximateFloat64() != 0 {
		return formatResourceQuantity(resourceName, requested)
	}
	return "-"
}

func resourceSeverity(pct float64) PodHealthSeverity {
	switch {
	case pct >= 0.90:
		return PodHealthCritical
	case pct >= 0.70:
		return PodHealthWarning
	default:
		return PodHealthHealthy
	}
}

func formatResourceQuantity(resourceName v1.ResourceName, quantity resource.Quantity) string {
	switch resourceName {
	case v1.ResourceCPU:
		return resource.NewMilliQuantity(quantity.MilliValue(), resource.DecimalSI).String()
	default:
		return formatBinaryQuantity(quantity)
	}
}

func formatBinaryQuantity(quantity resource.Quantity) string {
	value := quantity.Value()
	if value == 0 {
		return "0"
	}

	negative := value < 0
	if negative {
		value = -value
	}

	type binaryUnit struct {
		suffix string
		size   int64
	}

	units := []binaryUnit{
		{suffix: "Ki", size: 1 << 10},
		{suffix: "Mi", size: 1 << 20},
		{suffix: "Gi", size: 1 << 30},
		{suffix: "Ti", size: 1 << 40},
		{suffix: "Pi", size: 1 << 50},
		{suffix: "Ei", size: 1 << 60},
	}

	for i, unit := range units {
		scaled := divCeil(value, unit.size)
		if scaled < 10000 || i == len(units)-1 {
			if negative {
				scaled = -scaled
			}
			return fmt.Sprintf("%d%s", scaled, unit.suffix)
		}
	}

	return quantity.String()
}

func divCeil(numerator int64, denominator int64) int64 {
	if denominator == 0 {
		return 0
	}
	return (numerator + denominator - 1) / denominator
}

func formatPodUsageSummary(resourceName v1.ResourceName, used resource.Quantity, requested resource.Quantity, limits v1.ResourceList) (string, float64) {
	summary := summarizePodUsage(resourceName, used, requested, limits)
	return fmt.Sprintf("used %s req/lim %s/%s", summary.usedLabel, summary.requestedLabel, limitLabelForResource(resourceName, requested, limits)), summary.pct
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
		case "status":
			lhsHealth := lhs.Health()
			rhsHealth := rhs.Health()
			if lhsHealth.Severity == rhsHealth.Severity {
				if lhs.Restarts() == rhs.Restarts() {
					if lhsHealth.Label == rhsHealth.Label {
						return sortOrder(naturalLess(lhs.FullName(), rhs.FullName()))
					}
					return sortOrder(naturalLess(lhsHealth.Label, rhsHealth.Label))
				}
				return sortOrder(lhs.Restarts() < rhs.Restarts())
			}
			return sortOrder(lhsHealth.Severity < rhsHealth.Severity)
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
	for _, pod := range pods {
		req := pod.Requested()[resourceName]
		total.Add(req)
	}
	return total
}

func sumUsedResource(pods []*Pod, resourceName v1.ResourceName) resource.Quantity {
	total := resource.MustParse("0")
	for _, pod := range pods {
		used := pod.Usage()[resourceName]
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

func minInt(lhs, rhs int) int {
	if lhs < rhs {
		return lhs
	}
	return rhs
}

func maxInt(lhs, rhs int) int {
	if lhs > rhs {
		return lhs
	}
	return rhs
}
