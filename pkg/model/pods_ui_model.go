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
	"github.com/charmbracelet/x/ansi"
	"github.com/facette/natsort"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

var (
	podsHelpStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#71717A")).Render
	podsMutedStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#A1A1AA")).Render
	podsAliasStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#94A3B8")).Render
	podsBarShellStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#52525B")).Render
	podsCPUStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F97316")).Render
	podsMemoryStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#38BDF8")).Render
	podsDefaultResStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#22C55E")).Render
	podsSurfaceBg       = lipgloss.Color("#151821")
	podsSurfaceAltBg    = lipgloss.Color("#10131B")
	podsSurfaceText     = lipgloss.Color("#E4E4E7")
	podsSurfaceMuted    = lipgloss.Color("#94A3B8")
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
	ready       bool
	cordoned    bool
	pods        int
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
	cluster            *Cluster
	paginator          paginator.Model
	filterInput        textinput.Model
	height             int
	width              int
	podSorter          func(lhs, rhs *Pod) bool
	podSort            string
	style              *Style
	resources          []v1.ResourceName
	contextName        string
	namespace          string
	groupMode          groupMode
	showDetails        bool
	filtering          bool
	filterQuery        string
	kubeconfig         string
	kubeContext        string
	selectedPod        objectKey
	hasSelectedPod     bool
	selectionPinned    bool
	actionMenuOpen     bool
	actionMenuIndex    int
	confirmActionOpen  bool
	confirmAction      podActionKind
	confirmActionIndex int
	containerMenuOpen  bool
	containerMenuIndex int
	pendingAction      podActionKind
	viewerOpen         bool
	viewerLoading      bool
	viewerTitle        string
	viewerBody         string
	viewerScroll       int
	statusMu           sync.RWMutex
	statusLine         string
	statusUntil        time.Time
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
		width:       120,
		height:      32,
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
	return tea.Batch(tea.ClearScreen, podsTickCmd())
}

func (u *PodsUIModel) View() string {
	if u.viewerOpen {
		return u.renderViewer()
	}

	stats := u.cluster.Stats()
	state := u.buildPodListState()

	var b strings.Builder
	header := u.renderHeader(stats, state.visiblePods, state.filteredPods)
	if header != "" {
		b.WriteString(header)
		b.WriteByte('\n')
	}

	if len(state.filteredPods) == 0 {
		if strings.TrimSpace(u.filterQuery) != "" {
			fmt.Fprintf(&b, "No pods match %q.\n", u.filterQuery)
		} else {
			fmt.Fprintln(&b, "No live pods found yet.")
			fmt.Fprintln(&b, podsMutedStyle("Selection and pod actions become available as soon as pods appear."))
		}
	} else {
		if len(state.pages) > 0 && u.paginator.Page >= 0 && u.paginator.Page < len(state.pages) {
			u.renderPage(&b, state.pages[u.paginator.Page], state.selectedPod, state.nodeAliases)
		}
	}

	fmt.Fprintln(&b, u.paginator.View())
	u.writeFooter(&b)
	base := u.combineWithNodeUsagePanel(b.String(), stats.Nodes, state.visiblePods, state.filteredPods, state.nodeAliases)
	return strings.TrimRight(base+u.renderActionOverlay(state), "\n")
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
		fmt.Fprintln(w, renderBadge(status, lipgloss.Color(u.style.yellowHex), lipgloss.Color("#332613"), true))
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

func (u *PodsUIModel) renderHeader(stats Stats, visiblePods []*Pod, filteredPods []*Pod) string {
	leftPaneWidth := u.leftPaneWidth()
	if leftPaneWidth == 0 {
		leftPaneWidth = u.width
	}
	if leftPaneWidth <= 0 {
		leftPaneWidth = 96
	}

	overview := u.renderOverviewPanel(stats, visiblePods, filteredPods)
	controls := u.renderControlsPanel(filteredPods)

	header := joinSections(overview, controls)
	if leftPaneWidth >= 110 {
		controlsWidth := 42
		if leftPaneWidth < 150 {
			controlsWidth = 36
		}
		overviewWidth := leftPaneWidth - controlsWidth - 3
		if overviewWidth < 44 {
			overviewWidth = 44
		}
		header = strings.TrimRight(sideBySideFixed(overview, controls, overviewWidth, controlsWidth, 3), "\n")
	}

	return strings.TrimRight(header+"\n"+u.renderSectionDivider(), "\n")
}

func (u *PodsUIModel) renderOverviewPanel(stats Stats, visiblePods []*Pod, filteredPods []*Pod) string {
	namespace := u.namespace
	if namespace == "" {
		namespace = "all"
	}
	contextName := u.contextName
	if contextName == "" {
		contextName = "current"
	}

	unhealthy := countUnhealthyPods(visiblePods)
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s  %s\n",
		renderBadge("K8S PODS", lipgloss.Color("#0F172A"), lipgloss.Color("#67E8F9"), true),
		renderMetaField("Context", contextName),
		renderMetaField("Namespace", namespace),
	)
	fmt.Fprintf(&b, "%s  %s  %s  %s  %s\n",
		renderMetaField("Pods", fmt.Sprintf("%d", len(visiblePods))),
		renderMetaField("Shown", fmt.Sprintf("%d", len(filteredPods))),
		renderMetaFieldWithColor("Unhealthy", fmt.Sprintf("%d", unhealthy), u.unhealthyBadgeColor(unhealthy)),
		renderMetaField("Sort", u.sortLabel()),
		renderMetaField("Group", u.groupModeLabel()),
	)
	fmt.Fprintf(&b, "%s\n", renderMetaField("Time", time.Now().Format("15:04:05")))

	for _, res := range u.resources {
		allocatable := stats.AllocatableResources[res]
		used := sumUsedResource(visiblePods, res)
		pctUsed := quantityPct(used, allocatable)
		avgPct := averagePodUsagePct(visiblePods, res)

		line := fmt.Sprintf("%s %s  %s/%s  %s",
			u.resourceLabel(res),
			u.renderUsageBar(res, pctUsed, u.summaryBarWidth()),
			formatResourceQuantity(res, used),
			formatResourceQuantity(res, allocatable),
			renderBadge(
				fmt.Sprintf("avg %s", u.colorizePct(avgPct, resourceSeverity(avgPct))),
				podsSurfaceText,
				podsSurfaceAltBg,
				false,
			),
		)
		fmt.Fprintln(&b, renderSummaryStrip(line))
	}

	return strings.TrimRight(b.String(), "\n")
}

func (u *PodsUIModel) renderControlsPanel(filteredPods []*Pod) string {
	pageLabel := "1/1"
	if u.paginator.TotalPages > 0 {
		pageLabel = fmt.Sprintf("%d/%d", u.paginator.Page+1, u.paginator.TotalPages)
	}

	selectedLabel := "none"
	if u.hasSelectedPod {
		selectedLabel = fmt.Sprintf("%s/%s", u.selectedPod.namespace, u.selectedPod.name)
	}

	var b strings.Builder
	fmt.Fprintln(&b, renderMetaField("Page", pageLabel))
	fmt.Fprintln(&b, renderMetaField("Selected", selectedLabel))
	fmt.Fprintln(&b, renderShortcutLine("↑/↓  ←/→  enter", "Navigate + menu"))
	fmt.Fprintln(&b, renderShortcutLine("c/m/s", "Sort CPU MEM Status"))
	fmt.Fprintln(&b, renderShortcutLine("g", "Cycle grouping"))
	fmt.Fprintln(&b, renderShortcutLine("i", "Toggle details"))
	fmt.Fprintln(&b, renderShortcutLine("/", "Filter pods"))
	fmt.Fprintln(&b, renderShortcutLine("esc", "Clear filter"))
	fmt.Fprintln(&b, renderShortcutLine("q", "Quit"))
	if strings.TrimSpace(u.filterQuery) != "" {
		fmt.Fprintln(&b, renderMetaField("Filter", u.filterQuery))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (u *PodsUIModel) renderPage(w io.Writer, page []podGroup, selectedPod *Pod, nodeAliases map[string]string) {
	for groupIndex, group := range page {
		if group.showHeader {
			fmt.Fprintf(w, "%s %s\n",
				renderBadge(group.label, podsSurfaceText, podsSurfaceBg, true),
				renderBadge(fmt.Sprintf("%d pods", group.totalPods), podsSurfaceMuted, podsSurfaceAltBg, false),
			)
		}
		for _, pod := range group.pods {
			io.WriteString(w, u.renderPodBlock(pod, pod == selectedPod, nodeAliases))
		}
		if groupIndex < len(page)-1 {
			fmt.Fprintln(w)
		}
	}
}

func (u *PodsUIModel) renderPodBlock(pod *Pod, selected bool, nodeAliases map[string]string) string {
	health := pod.Health()
	severity := podAttentionSeverity(pod, u.resources)
	name := pod.Name()
	if u.groupMode == groupModeFlat {
		name = pod.FullName()
	}
	name = u.renderPodName(name, pod)

	nodeSummary := renderBadge("pending", lipgloss.Color(u.style.yellowHex), lipgloss.Color("#332613"), true)
	if pod.NodeName() != "" {
		nodeSummary = renderBadge(nodeAlias(nodeAliases, pod.NodeName()), podsSurfaceMuted, podsSurfaceAltBg, false)
		if u.showDetails {
			nodeSummary = renderBadge(fmt.Sprintf("%s (%s)", nodeAlias(nodeAliases, pod.NodeName()), pod.NodeName()), podsSurfaceMuted, podsSurfaceAltBg, false)
		}
	}

	extras := []string{
		u.renderHealth(health),
		nodeSummary,
	}
	if restarts := pod.Restarts(); restarts > 0 {
		extras = append(extras, renderBadge(fmt.Sprintf("%dr", restarts), lipgloss.Color(u.style.yellowHex), lipgloss.Color("#332613"), true))
	}

	marker := "▎"
	nameStyle := lipgloss.NewStyle().Foreground(u.style.SeverityColor(severity)).Bold(true)
	if selected {
		marker = "▶"
		nameStyle = nameStyle.Background(lipgloss.Color("#172033")).Padding(0, 1)
	}
	name = nameStyle.Render(name)

	var b strings.Builder
	fmt.Fprintf(&b, "  %s %s  %s\n",
		lipgloss.NewStyle().Foreground(u.style.SeverityColor(severity)).Bold(true).Render(marker),
		name,
		strings.Join(extras, " "),
	)
	usage := pod.Usage()
	requested := pod.Requested()
	limits := pod.Limits()
	for _, res := range u.resources {
		summary := summarizePodUsage(res, usage[res], requested[res], limits)
		fmt.Fprintf(&b, "    %s %s  %s/%s",
			u.resourceLabel(res),
			u.renderUsageBar(res, summary.pct, u.resourceBarWidth()),
			formatResourceQuantity(res, usage[res]),
			summary.baselineLabel,
		)
		if u.showDetails {
			fmt.Fprintf(&b, "  %s %s",
				renderBadge(fmt.Sprintf("req %s", summary.requestedLabel), podsSurfaceMuted, podsSurfaceAltBg, false),
				renderBadge(fmt.Sprintf("lim %s", limitLabelForResource(res, requested[res], limits)), podsSurfaceMuted, podsSurfaceAltBg, false),
			)
		}
		fmt.Fprintln(&b)
	}

	block := b.String()
	if selected && (u.actionMenuOpen || u.containerMenuOpen || u.confirmActionOpen) {
		block = sideBySide(strings.TrimRight(block, "\n"), u.renderActionPopover(pod), 3)
	}
	return block
}

func (u *PodsUIModel) writeFooter(w io.Writer) {
	if u.filtering {
		fmt.Fprintf(w, "%s %s\n", renderBadge("Filter", podsSurfaceText, podsSurfaceBg, true), u.filterInput.View())
	} else if strings.TrimSpace(u.filterQuery) != "" {
		fmt.Fprintf(w, "%s %s\n",
			renderBadge("Filter", podsSurfaceText, podsSurfaceBg, true),
			renderBadge(u.filterQuery, podsSurfaceMuted, podsSurfaceAltBg, false),
		)
	}
	u.writeStatusLine(w)
	fmt.Fprintln(w, podsHelpStyle("nav ↑/↓ ←/→ enter • sort c/m/s • group g • info i • filter / • quit q"))
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
			separatorCost := 0
			if len(page) > 0 {
				separatorCost = 1
			}
			headerCost := 0
			if group.showHeader {
				headerCost = 1
			}

			required := linesPerPod + headerCost + separatorCost
			if remaining < required && len(page) > 0 {
				flushPage()
			}
			separatorCost = 0
			if len(page) > 0 {
				separatorCost = 1
			}
			required = linesPerPod + headerCost + separatorCost
			if remaining < required {
				remaining = required
			}

			fit := (remaining - headerCost - separatorCost) / linesPerPod
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
			remaining -= separatorCost + headerCost + fit*linesPerPod
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
		if u.viewerOpen {
			return u.handleViewerKey(msg)
		}
		if u.confirmActionOpen {
			return u.handleConfirmActionKey(msg)
		}
		if u.containerMenuOpen {
			return u.handleContainerMenuKey(msg)
		}
		if u.actionMenuOpen {
			return u.handleActionMenuKey(msg)
		}
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
				u.resetSelectionAnchor()
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
		case "up", "k":
			u.selectPodByOffset(-1)
			return u, nil
		case "down", "j":
			u.selectPodByOffset(1)
			return u, nil
		case "left", "h":
			if u.selectPage(u.paginator.Page - 1) {
				return u, nil
			}
			return u, nil
		case "right", "l":
			if u.selectPage(u.paginator.Page + 1) {
				return u, nil
			}
			return u, nil
		case "enter":
			u.openActionMenu()
			return u, nil
		case "c":
			u.setPodSorter("cpu=dsc")
			u.resetSelectionAnchor()
			u.paginator.Page = 0
			u.SetTransientStatus("Sorted by CPU.", 2*time.Second)
			return u, nil
		case "m":
			u.setPodSorter("memory=dsc")
			u.resetSelectionAnchor()
			u.paginator.Page = 0
			u.SetTransientStatus("Sorted by memory.", 2*time.Second)
			return u, nil
		case "s":
			u.setPodSorter("status=dsc")
			u.resetSelectionAnchor()
			u.paginator.Page = 0
			u.SetTransientStatus("Sorted by status.", 2*time.Second)
			return u, nil
		case "g":
			u.groupMode = u.groupMode.next()
			u.resetSelectionAnchor()
			u.paginator.Page = 0
			u.SetTransientStatus(fmt.Sprintf("Grouping by %s.", u.groupModeLabel()), 2*time.Second)
			return u, nil
		case "i":
			u.showDetails = !u.showDetails
			u.resetSelectionAnchor()
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
	case podActionOutputMsg:
		u.viewerLoading = false
		if msg.err != nil {
			if strings.TrimSpace(msg.body) == "" {
				u.viewerBody = fmt.Sprintf("Command failed: %v", msg.err)
			} else {
				u.viewerBody = fmt.Sprintf("Command failed: %v\n\n%s", msg.err, msg.body)
			}
		} else {
			u.viewerBody = msg.body
		}
		u.viewerTitle = msg.title
		u.viewerScroll = 0
		return u, nil
	case podExecFinishedMsg:
		if msg.err != nil {
			u.SetTransientStatus(fmt.Sprintf("Exec session ended: %v", msg.err), 4*time.Second)
		} else {
			u.SetTransientStatus("Exec session closed.", 2*time.Second)
		}
		return u, nil
	case podMutationMsg:
		if msg.err != nil {
			u.SetTransientStatus(msg.err.Error(), 6*time.Second)
		} else if strings.TrimSpace(msg.status) != "" {
			u.SetTransientStatus(msg.status, 4*time.Second)
		}
		return u, nil
	}
	return u, nil
}

func (u *PodsUIModel) handleFilteringKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return u, tea.Quit
	case "esc", "enter":
		u.filtering = false
		u.filterInput.Blur()
		u.filterQuery = strings.TrimSpace(u.filterInput.Value())
		u.resetSelectionAnchor()
		u.paginator.Page = 0
		return u, nil
	}

	var cmd tea.Cmd
	u.filterInput, cmd = u.filterInput.Update(msg)
	u.filterQuery = strings.TrimSpace(u.filterInput.Value())
	u.resetSelectionAnchor()
	u.paginator.Page = 0
	return u, cmd
}

func (u *PodsUIModel) combineWithNodeUsagePanel(left string, nodes []*Node, pods []*Pod, filteredPods []*Pod, nodeAliases map[string]string) string {
	if u.width > 0 && u.width < 150 {
		return left
	}

	if u.width <= 0 {
		return left
	}

	nodeSection := u.renderNodeUsagePanel(nodes, pods, nodeAliases)
	if strings.TrimSpace(nodeSection) == "" {
		nodeSection = u.renderNodeUsagePlaceholder()
	}
	rightSections := []string{
		u.renderSignalsPanel(nodes, pods, filteredPods),
		nodeSection,
	}
	right := strings.TrimSpace(joinSections(rightSections...))
	return sideBySideFixed(left, right, u.leftPaneWidth(), u.rightPaneWidth(), 3)
}

func (u *PodsUIModel) renderNodeUsagePanel(nodes []*Node, pods []*Pod, nodeAliases map[string]string) string {
	if len(nodes) == 0 {
		return ""
	}
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
			ready:       node.Ready(),
			cordoned:    node.Cordoned(),
			pods:        node.NumPods(),
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
	fmt.Fprintln(&b, renderBadge("NODE PRESSURE", lipgloss.Color("#0F172A"), lipgloss.Color("#7DD3FC"), true))
	for _, row := range rows {
		name := renderBadge(row.alias, podsSurfaceText, podsSurfaceAltBg, true)
		if u.showDetails {
			name = renderBadge(fmt.Sprintf("%s (%s)", row.alias, row.name), podsSurfaceText, podsSurfaceAltBg, true)
		}
		parts := []string{
			name,
			renderBadge(fmt.Sprintf("%d pods", row.pods), podsSurfaceMuted, podsSurfaceAltBg, false),
		}
		if !row.ready {
			parts = append(parts, renderBadge("NotReady", lipgloss.Color(u.style.redHex), lipgloss.Color("#33191E"), true))
		}
		if row.cordoned {
			parts = append(parts, renderBadge("Cordoned", lipgloss.Color(u.style.yellowHex), lipgloss.Color("#332613"), true))
		}
		fmt.Fprintln(&b, strings.Join(parts, " "))
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

func (u *PodsUIModel) renderSignalsPanel(nodes []*Node, pods []*Pod, filteredPods []*Pod) string {
	rightWidth := u.rightPaneWidth()
	if rightWidth == 0 {
		return ""
	}

	pageLabel := "1/1"
	if u.paginator.TotalPages > 0 {
		pageLabel = fmt.Sprintf("%d/%d", u.paginator.Page+1, u.paginator.TotalPages)
	}

	var b strings.Builder
	fmt.Fprintln(&b, renderBadge("HIGHLIGHTS", lipgloss.Color("#0F172A"), lipgloss.Color("#A78BFA"), true))
	fmt.Fprintln(&b, strings.Join([]string{
		renderBadge(fmt.Sprintf("%d nodes", len(nodes)), podsSurfaceText, podsSurfaceAltBg, true),
		renderBadge(fmt.Sprintf("%d shown", len(filteredPods)), podsSurfaceText, podsSurfaceAltBg, true),
		renderBadge(fmt.Sprintf("page %s", pageLabel), podsSurfaceMuted, podsSurfaceAltBg, false),
	}, " "))

	if pod, pct := u.topUsagePod(filteredPods, v1.ResourceCPU); pod != nil {
		fmt.Fprintln(&b, u.renderSignalRow("CPU hot", pod.Name(), fmt.Sprintf("%.0f%%", pct*100), PodHealthWarning, rightWidth))
	}
	if pod, pct := u.topUsagePod(filteredPods, v1.ResourceMemory); pod != nil {
		fmt.Fprintln(&b, u.renderSignalRow("MEM hot", pod.Name(), fmt.Sprintf("%.0f%%", pct*100), PodHealthHealthy, rightWidth))
	}
	if pod, restarts := topRestartPod(filteredPods); pod != nil && restarts > 0 {
		fmt.Fprintln(&b, u.renderSignalRow("Restarts", pod.Name(), fmt.Sprintf("%dr", restarts), PodHealthWarning, rightWidth))
	} else {
		fmt.Fprintln(&b, u.renderSignalRow("Restarts", "Stable", "0r", PodHealthHealthy, rightWidth))
	}
	if pod := firstUnhealthyPod(filteredPods); pod != nil {
		fmt.Fprintln(&b, u.renderSignalRow("Watch", pod.Name(), pod.Health().Label, pod.Health().Severity, rightWidth))
	} else {
		fmt.Fprintln(&b, u.renderSignalRow("Watch", "Cluster healthy", "OK", PodHealthHealthy, rightWidth))
	}

	return strings.TrimRight(b.String(), "\n")
}

func (u *PodsUIModel) renderNodeUsagePlaceholder() string {
	var b strings.Builder
	fmt.Fprintln(&b, renderBadge("NODE PRESSURE", lipgloss.Color("#0F172A"), lipgloss.Color("#7DD3FC"), true))
	fmt.Fprintln(&b, renderBadge("Waiting for node data...", podsSurfaceMuted, podsSurfaceAltBg, false))
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
	fg, bg := u.severityBadgeColors(health.Severity)
	return renderBadge(fmt.Sprintf("%s %s", health.Icon, health.Label), fg, bg, true)
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
	label := fmt.Sprintf("%.0f%%", pct*100)
	if pct*100 >= 1000 {
		label = "999%+"
	}

	boundedPct := boundPct(pct)
	filled := int(math.Round(boundedPct * float64(width)))
	warnAt := int(math.Round(0.7 * float64(width)))
	criticalAt := int(math.Round(0.9 * float64(width)))
	fillBG, emptyBG, markerBG := resourceBarPalette(resourceName)

	cells := make([]string, width)
	for i := 0; i < width; i++ {
		background := emptyBG
		if i == warnAt-1 || i == criticalAt-1 {
			background = markerBG
		}
		if i < filled {
			background = fillBG
		}
		cells[i] = lipgloss.NewStyle().Background(background).Render(" ")
	}

	if len(label) < width {
		start := (width - len(label)) / 2
		for offset, r := range label {
			index := start + offset
			if index >= 0 && index < width {
				background := emptyBG
				if index == warnAt-1 || index == criticalAt-1 {
					background = markerBG
				}
				labelFG := podsSurfaceText
				if index < filled {
					background = fillBG
					labelFG = lipgloss.Color("#0F172A")
				}
				cells[index] = lipgloss.NewStyle().
					Foreground(labelFG).
					Background(background).
					Bold(true).
					Render(string(r))
			}
		}
	}

	return podsBarShellStyle("▕") + strings.Join(cells, "") + podsBarShellStyle("▏")
}

func resourceBarPalette(resourceName v1.ResourceName) (fill lipgloss.Color, empty lipgloss.Color, marker lipgloss.Color) {
	switch resourceName {
	case v1.ResourceCPU:
		return lipgloss.Color("#FB923C"), lipgloss.Color("#1B1E28"), lipgloss.Color("#2A1F18")
	case v1.ResourceMemory:
		return lipgloss.Color("#38BDF8"), lipgloss.Color("#1B1E28"), lipgloss.Color("#1A2634")
	default:
		return lipgloss.Color("#22C55E"), lipgloss.Color("#1B1E28"), lipgloss.Color("#173124")
	}
}

func (u *PodsUIModel) colorizePct(pct float64, severity PodHealthSeverity) string {
	return lipgloss.NewStyle().Foreground(u.style.SeverityColor(severity)).Bold(true).Render(fmt.Sprintf("%.0f%%", pct*100))
}

func (u *PodsUIModel) summaryBarWidth() int {
	switch {
	case u.width >= 190:
		return 28
	case u.width >= 150:
		return 24
	default:
		return 18
	}
}

func (u *PodsUIModel) resourceBarWidth() int {
	leftWidth := u.leftPaneWidth()
	if leftWidth == 0 {
		leftWidth = u.width
	}
	if leftWidth <= 0 {
		return 18
	}

	target := leftWidth / 2
	if u.showDetails {
		target = leftWidth / 3
	}

	maxWidth := leftWidth - 28
	if u.showDetails {
		maxWidth = leftWidth - 48
	}

	if maxWidth < 18 {
		maxWidth = 18
	}
	if target > maxWidth {
		target = maxWidth
	}
	if target < 18 {
		target = 18
	}

	return target
}

func (u *PodsUIModel) nodeBarWidth() int {
	switch {
	case u.width >= 190:
		return 24
	case u.width >= 150:
		return 20
	default:
		return 18
	}
}

func (u *PodsUIModel) rightPaneWidth() int {
	switch {
	case u.width >= 190:
		return 72
	case u.width >= 150:
		return 64
	default:
		return 0
	}
}

func (u *PodsUIModel) leftPaneWidth() int {
	rightWidth := u.rightPaneWidth()
	if rightWidth == 0 || u.width <= 0 {
		return 0
	}
	leftWidth := u.width - rightWidth - 3
	if leftWidth < 60 {
		return 60
	}
	return leftWidth
}

func (u *PodsUIModel) linesPerPod() int {
	return 1 + len(u.resources)
}

func (u *PodsUIModel) availablePodLines() int {
	height := u.height
	if height <= 0 {
		height = 32
	}

	footerLines := 2
	if strings.TrimSpace(u.filterQuery) != "" || u.filtering {
		footerLines++
	}
	if u.currentStatus() != "" {
		footerLines++
	}

	headerLines := renderedLineCount(u.renderHeader(u.cluster.Stats(), u.cluster.VisiblePods(), u.filterPods(u.cluster.VisiblePods())))
	lines := height - headerLines - footerLines
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

func (u *PodsUIModel) renderSectionDivider() string {
	width := u.width
	if leftWidth := u.leftPaneWidth(); leftWidth > 0 {
		width = leftWidth
	}
	if width <= 0 {
		width = 72
	}
	width -= 2
	if width < 18 {
		width = 18
	}
	divider := strings.Repeat("━", width)
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#272A36")).Render(divider)
}

func renderMetaField(label string, value string) string {
	return renderMetaFieldWithColor(label, value, podsSurfaceText)
}

func renderMetaFieldWithColor(label string, value string, valueColor lipgloss.TerminalColor) string {
	labelPart := lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B")).Render(label + ":")
	valuePart := lipgloss.NewStyle().Foreground(valueColor).Bold(true).Render(value)
	return labelPart + " " + valuePart
}

func renderShortcutLine(key string, label string) string {
	return renderBadge(key, lipgloss.Color("#60A5FA"), lipgloss.Color("#111827"), true) + " " +
		lipgloss.NewStyle().Foreground(podsSurfaceMuted).Render(label)
}

func (u *PodsUIModel) renderSignalRow(label string, name string, value string, severity PodHealthSeverity, panelWidth int) string {
	fg, bg := u.severityBadgeColors(severity)
	nameWidth := panelWidth - len(label) - len(value) - 10
	if nameWidth < 12 {
		nameWidth = 12
	}
	name = truncateRunes(name, nameWidth)
	return strings.Join([]string{
		renderBadge(label, fg, bg, true),
		renderBadge(name, podsSurfaceText, podsSurfaceBg, true),
		renderBadge(value, podsSurfaceMuted, podsSurfaceAltBg, false),
	}, " ")
}

func renderSummaryStrip(content string) string {
	return lipgloss.NewStyle().
		Background(lipgloss.Color("#11141D")).
		Foreground(podsSurfaceText).
		Padding(0, 1).
		Render(content)
}

func renderBadge(text string, fg lipgloss.TerminalColor, bg lipgloss.TerminalColor, bold bool) string {
	style := lipgloss.NewStyle().Foreground(fg).Background(bg).Padding(0, 1)
	if bold {
		style = style.Bold(true)
	}
	return style.Render(text)
}

func joinSections(parts ...string) string {
	sections := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		sections = append(sections, strings.TrimSpace(part))
	}
	return strings.Join(sections, "\n\n")
}

func renderedLineCount(text string) int {
	trimmed := strings.TrimRight(text, "\n")
	if trimmed == "" {
		return 0
	}
	return strings.Count(trimmed, "\n") + 1
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if limit <= 0 || len(runes) <= limit {
		return text
	}
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}

func (u *PodsUIModel) severityBadgeColors(severity PodHealthSeverity) (lipgloss.Color, lipgloss.Color) {
	switch severity {
	case PodHealthCritical:
		return lipgloss.Color(u.style.redHex), lipgloss.Color("#33191E")
	case PodHealthWarning:
		return lipgloss.Color(u.style.yellowHex), lipgloss.Color("#332613")
	default:
		return lipgloss.Color(u.style.greenHex), lipgloss.Color("#0F2A22")
	}
}

func (u *PodsUIModel) unhealthyBadgeColor(unhealthy int) lipgloss.Color {
	switch {
	case unhealthy >= 3:
		return lipgloss.Color(u.style.redHex)
	case unhealthy > 0:
		return lipgloss.Color(u.style.yellowHex)
	default:
		return lipgloss.Color(u.style.greenHex)
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

func firstUnhealthyPod(pods []*Pod) *Pod {
	for _, pod := range pods {
		if pod.Health().Severity > PodHealthHealthy {
			return pod
		}
	}
	return nil
}

func topRestartPod(pods []*Pod) (*Pod, int) {
	var top *Pod
	maxRestarts := 0
	for _, pod := range pods {
		restarts := pod.Restarts()
		if top == nil || restarts > maxRestarts {
			top = pod
			maxRestarts = restarts
		}
	}
	return top, maxRestarts
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

func (u *PodsUIModel) topUsagePod(pods []*Pod, resourceName v1.ResourceName) (*Pod, float64) {
	var top *Pod
	topPct := 0.0
	for _, pod := range pods {
		summary := summarizePodUsage(resourceName, pod.Usage()[resourceName], pod.Requested()[resourceName], pod.Limits())
		if top == nil || summary.pct > topPct {
			top = pod
			topPct = summary.pct
		}
	}
	return top, topPct
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

func sideBySideFixed(left, right string, leftWidth, rightWidth, gap int) string {
	left = strings.TrimRight(left, "\n")
	right = strings.TrimRight(right, "\n")
	leftLines := strings.Split(left, "\n")
	rightLines := strings.Split(right, "\n")
	maxLines := int(math.Max(float64(len(leftLines)), float64(len(rightLines))))

	var b strings.Builder
	separator := strings.Repeat(" ", gap)
	if gap >= 3 {
		separator = " " + lipgloss.NewStyle().Foreground(lipgloss.Color("#272A36")).Render("│") + " "
	}
	for i := 0; i < maxLines; i++ {
		leftLine := ""
		rightLine := ""
		if i < len(leftLines) {
			leftLine = fitANSIWidth(leftLines[i], leftWidth)
		}
		if i < len(rightLines) {
			rightLine = fitANSIWidth(rightLines[i], rightWidth)
		}

		leftPadding := 0
		if leftWidth > 0 {
			leftPadding = leftWidth - ansi.StringWidth(leftLine)
			if leftPadding < 0 {
				leftPadding = 0
			}
		}

		fmt.Fprintf(&b, "%s%s%s%s\n", leftLine, strings.Repeat(" ", leftPadding), separator, rightLine)
	}
	return b.String()
}

func fitANSIWidth(line string, width int) string {
	if width <= 0 {
		return line
	}
	if ansi.StringWidth(line) <= width {
		return line
	}
	return ansi.Truncate(line, width, "")
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
