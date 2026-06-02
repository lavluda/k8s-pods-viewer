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

// Direction B — "Paneled Console" palette. Soft-bordered panels on a near-black
// background, accent green selection, orange CPU, cyan MEM. Colors are derived
// from k8s-pods-viewer-Directions.html / DirectionB.jsx.
var (
	podsHelpStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7689")).Render
	podsMutedStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#8893A3")).Render
	podsBarShellStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#1C2230")).Render
	podsCPUStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E3873C")).Render
	podsMemoryStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#56C2E6")).Render
	podsDefaultResStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#39D353")).Render

	// Panel chrome.  The design's #1C2230 border is fine in a browser but
	// invisible in a terminal.  #5B6880 gives enough contrast against #0B0E14
	// while still reading as a "quiet" border rather than a bold line.
	podsPanelBg       = lipgloss.Color("#0F141B")
	podsPanelAltBg    = lipgloss.Color("#11161E")
	podsPanelBorder   = lipgloss.Color("#5B6880")
	podsPanelBorderHi = lipgloss.Color("#5B7FAF") // action-popover / selection
	podsTitleFG       = lipgloss.Color("#6B7689")
	podsSurfaceBg     = lipgloss.Color("#151B25")
	podsSurfaceAltBg  = lipgloss.Color("#11161E")
	podsSurfaceText   = lipgloss.Color("#E6EDF3")
	podsSurfaceMuted  = lipgloss.Color("#8893A3")
	podsSurfaceDim    = lipgloss.Color("#4D5666")

	// Resource accent colors (CPU orange, MEM cyan, default green) reused for
	// labels, bars, and KPI tile values.
	podsCPUColor     = lipgloss.Color("#E3873C")
	podsCPUEmpty     = lipgloss.Color("#252D3D")
	podsMemColor     = lipgloss.Color("#56C2E6")
	podsMemEmpty     = lipgloss.Color("#252D3D")
	podsDefaultColor = lipgloss.Color("#39D353")

	// Paginator dots
	podsActiveDot   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "235", Dark: "252"}).Render("•")
	podsInactiveDot = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "250", Dark: "238"}).Render("•")
)

type groupMode string

const (
	groupModeNamespace groupMode = "namespace"
	groupModeWorkload  groupMode = "workload"
	groupModeFlat      groupMode = "flat"
)

const (
	podsTickInterval         = 2 * time.Second
	podAutoSortKeyboardPause = 30 * time.Second
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
	cluster             *Cluster
	paginator           paginator.Model
	filterInput         textinput.Model
	height              int
	width               int
	podSorter           func(lhs, rhs *Pod) bool
	podSort             string
	podOrder            []objectKey
	autoSortPausedUntil time.Time
	style               *Style
	resources           []v1.ResourceName
	contextName         string
	namespace           string
	groupMode           groupMode
	showDetails         bool
	filtering           bool
	filterQuery         string
	kubeconfig          string
	kubeContext         string
	selectedPod         objectKey
	hasSelectedPod      bool
	selectionPinned     bool
	actionMenuOpen      bool
	actionMenuIndex     int
	confirmActionOpen   bool
	confirmAction       podActionKind
	confirmActionIndex  int
	containerMenuOpen   bool
	containerMenuIndex  int
	pendingAction       podActionKind
	viewerOpen          bool
	viewerLoading       bool
	viewerTitle         string
	viewerBody          string
	viewerScroll        int
	statusMu            sync.RWMutex
	statusLine          string
	statusUntil         time.Time
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

	header := u.renderHeader(stats, state.visiblePods, state.filteredPods)

	var body strings.Builder
	if len(state.filteredPods) == 0 {
		if strings.TrimSpace(u.filterQuery) != "" {
			fmt.Fprintf(&body, "No pods match %q.\n", u.filterQuery)
		} else {
			fmt.Fprintln(&body, "No live pods found yet.")
			fmt.Fprintln(&body, podsMutedStyle("Selection and pod actions become available as soon as pods appear."))
		}
	} else {
		if len(state.pages) > 0 && u.paginator.Page >= 0 && u.paginator.Page < len(state.pages) {
			u.renderPage(&body, state.pages[u.paginator.Page], state.selectedPod, state.nodeAliases)
		}
	}

	var footer strings.Builder
	fmt.Fprintln(&footer, u.paginator.View())
	u.writeFooter(&footer)

	combinedBody := u.combineWithNodeUsagePanel(body.String(), stats.Nodes, state.visiblePods, state.filteredPods, state.nodeAliases)

	// Paint the action popup as a floating overlay on the combined body so the
	// pod list height and the sidebar both remain completely unchanged.
	if (u.actionMenuOpen || u.containerMenuOpen || u.confirmActionOpen) && state.selectedPod != nil {
		const popupWidth = 46
		leftWidth := u.leftPaneWidth()
		if leftWidth <= 0 {
			leftWidth = u.width
		}
		startCol := leftWidth - popupWidth - 3
		if startCol < 20 {
			startCol = 20
		}
		popup := u.renderActionPopover(state.selectedPod, popupWidth)
		// row 2 inside combinedBody = inside the pod panel, just past the title
		// and group-header lines, so the popup floats near the selected pod.
		combinedBody = overlayAt(combinedBody, popup, 2, startCol)
	}

	out := strings.TrimRight(header+"\n"+combinedBody+footer.String(), "\n")
	return strings.TrimRight(out+u.renderActionOverlay(state), "\n")
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

// renderHeader paints the Direction-B top bar (logo, ctx/ns, filter, time)
// followed by a compact one-line KPI strip.
func (u *PodsUIModel) renderHeader(stats Stats, visiblePods []*Pod, filteredPods []*Pod) string {
	headerWidth := u.headerWidth()
	topBar := u.renderTopBar(headerWidth)
	kpis := u.renderKpiStrip(stats, visiblePods, filteredPods, headerWidth)
	return strings.TrimRight(topBar+"\n"+kpis, "\n")
}

func (u *PodsUIModel) renderTopBar(width int) string {
	if width < 40 {
		width = 40
	}

	accent := u.style.Accent()
	logo := lipgloss.NewStyle().Foreground(accent).Bold(true).Render("■") + " " +
		lipgloss.NewStyle().Foreground(podsSurfaceText).Bold(true).Render("k8s-pods-viewer")

	contextName := u.contextName
	if contextName == "" {
		contextName = "current"
	}
	namespace := u.namespace
	if namespace == "" {
		namespace = "all"
	}
	// Keep "Context: X" and "Namespace: Y" literal substrings so anything
	// grepping the rendered output (tests, scripts) keeps working.
	ctxBar := lipgloss.NewStyle().Foreground(podsTitleFG).Render("Context: ") +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#D2A8FF")).Bold(true).Render(contextName) +
		"  " +
		lipgloss.NewStyle().Foreground(podsTitleFG).Render("Namespace: ") +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#A5D6FF")).Bold(true).Render(namespace)

	filterText := "filter pods…"
	if strings.TrimSpace(u.filterQuery) != "" {
		filterText = u.filterQuery
	}
	filterChip := lipgloss.NewStyle().
		Foreground(podsSurfaceMuted).
		Background(podsPanelBg).
		Padding(0, 1).
		Render(lipgloss.NewStyle().Foreground(accent).Render("/") + " " + filterText)

	timeChip := lipgloss.NewStyle().Foreground(podsTitleFG).Render(time.Now().Format("15:04:05"))

	left := logo + "  " + ctxBar
	right := filterChip + "  " + timeChip
	gap := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 2 {
		// not enough room: stack on two lines
		return left + "\n" + right
	}
	return left + strings.Repeat(" ", gap) + right
}

// renderKpiStrip renders 4-line bordered tiles that match the Direction-B
// design: the tile label sits in the top border, then two content rows (big
// value + sub-text), then bottom border.  Each tile is joined side-by-side
// with lipgloss.JoinHorizontal so multi-line blocks align automatically.
// The "Shown: N" literal is kept so tests/scripts that grep for it still work.
func (u *PodsUIModel) renderKpiStrip(stats Stats, visiblePods []*Pod, filteredPods []*Pod, width int) string {
	unhealthy := countUnhealthyPods(visiblePods)
	warn, crit := splitUnhealthy(visiblePods)

	type kpiSpec struct {
		label string
		value string
		sub   string
		fg    lipgloss.Color
	}
	specs := []kpiSpec{
		{
			"PODS",
			fmt.Sprintf("%d", len(visiblePods)),
			fmt.Sprintf("Shown: %d", len(filteredPods)),
			podsSurfaceText,
		},
		{
			"UNHEALTHY",
			fmt.Sprintf("%d", unhealthy),
			fmt.Sprintf("%d warn · %d crit", warn, crit),
			u.unhealthyBadgeColor(unhealthy),
		},
	}
	for _, res := range u.resources {
		allocatable := stats.AllocatableResources[res]
		used := sumUsedResource(visiblePods, res)
		pctUsed := quantityPct(used, allocatable)
		fg := podsCPUColor
		if res == v1.ResourceMemory {
			fg = podsMemColor
		}
		specs = append(specs, kpiSpec{
			"CLUSTER " + strings.ToUpper(string(res)),
			fmt.Sprintf("%.0f%%", pctUsed*100),
			fmt.Sprintf("%s/%s", formatResourceQuantity(res, used), formatResourceQuantity(res, allocatable)),
			fg,
		})
	}

	n := len(specs)
	if n == 0 {
		return ""
	}
	tileWidth := (width - (n - 1)) / n
	if tileWidth < 18 {
		tileWidth = 18
	}

	tiles := make([]string, n)
	for i, s := range specs {
		valueLine := lipgloss.NewStyle().Foreground(s.fg).Bold(true).Render(s.value)
		subLine := lipgloss.NewStyle().Foreground(podsSurfaceDim).Render(s.sub)
		tiles[i] = renderPanel(tileWidth, s.label, "", valueLine+"\n"+subLine, podsPanelBorder)
	}

	// Interleave tiles with 1-space separators so JoinHorizontal lines up rows.
	args := make([]string, 0, n*2-1)
	for i, t := range tiles {
		if i > 0 {
			args = append(args, " ")
		}
		args = append(args, t)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, args...)
}

func splitUnhealthy(pods []*Pod) (warn int, crit int) {
	for _, pod := range pods {
		switch pod.Health().Severity {
		case PodHealthCritical:
			crit++
		case PodHealthWarning:
			warn++
		}
	}
	return
}


func (u *PodsUIModel) renderPage(w io.Writer, page []podGroup, selectedPod *Pod, nodeAliases map[string]string) {
	width := u.podPanelWidth()
	var inner strings.Builder
	for groupIndex, group := range page {
		if group.showHeader {
			fmt.Fprintf(&inner, "%s  %s\n",
				lipgloss.NewStyle().Foreground(u.style.Accent()).Render("▸ "+group.label),
				lipgloss.NewStyle().Foreground(podsSurfaceDim).Render(fmt.Sprintf("%d pods", group.totalPods)),
			)
		}
		for _, pod := range group.pods {
			inner.WriteString(u.renderPodBlock(pod, pod == selectedPod, nodeAliases))
		}
		if groupIndex < len(page)-1 {
			fmt.Fprintln(&inner)
		}
	}

	title := "Pods · Grouped By " + u.groupModeLabel()
	if u.groupMode == groupModeFlat {
		title = "Pods · Flat"
	}
	sortArrow := "↓"
	if strings.HasSuffix(u.podSort, "=asc") {
		sortArrow = "↑"
	}
	right := fmt.Sprintf("sort %s %s", u.sortLabel(), sortArrow)
	io.WriteString(w, renderPanel(width, title, right, strings.TrimRight(inner.String(), "\n"), podsPanelBorder))
	io.WriteString(w, "\n")
}

// renderPodBlock paints one pod row group: a name line (dot, name, status,
// restarts, node) followed by per-resource bar lines. Selection paints a
// green left-accent gutter; the bar style obeys u.style.BarStyle().
func (u *PodsUIModel) renderPodBlock(pod *Pod, selected bool, nodeAliases map[string]string) string {
	health := pod.Health()
	dot := u.style.SeverityColor(health.Severity)
	accent := u.style.Accent()

	name := pod.Name()
	if u.groupMode == groupModeFlat {
		name = pod.FullName()
	}
	nameRender := lipgloss.NewStyle().
		Foreground(podsSurfaceText).
		Bold(true).
		Render(name)
	if selected {
		nameRender = lipgloss.NewStyle().
			Foreground(podsSurfaceText).
			Background(lipgloss.Color("#0F2218")).
			Bold(true).
			Render(name)
	}

	statusTxt := lipgloss.NewStyle().Foreground(dot).Render(health.Label)
	nodeTxt := lipgloss.NewStyle().Foreground(podsSurfaceDim).Render("·  pending")
	if pod.NodeName() != "" {
		alias := nodeAlias(nodeAliases, pod.NodeName())
		if u.showDetails {
			alias = fmt.Sprintf("%s (%s)", alias, pod.NodeName())
		}
		nodeTxt = lipgloss.NewStyle().Foreground(podsSurfaceDim).Render("·  ") +
			lipgloss.NewStyle().Foreground(podsSurfaceMuted).Render(alias)
	}
	restartsTxt := ""
	if r := pod.Restarts(); r > 0 {
		restartsTxt = "  " + renderBadge(fmt.Sprintf("%dr", r), lipgloss.Color(u.style.yellowHex), lipgloss.Color("#332613"), true)
	}

	// Selection indicator: accent left-bar for selected, plain space for others.
	leftBar := "  "
	if selected {
		leftBar = lipgloss.NewStyle().Foreground(accent).Bold(true).Render("▌") + " "
	}
	dotMark := lipgloss.NewStyle().Foreground(dot).Render("●")

	var b strings.Builder
	fmt.Fprintf(&b, "%s%s  %s   %s  %s%s\n", leftBar, dotMark, nameRender, statusTxt, nodeTxt, restartsTxt)

	usage := pod.Usage()
	requested := pod.Requested()
	limits := pod.Limits()
	barWidth := u.resourceBarWidth()
	for _, res := range u.resources {
		summary := summarizePodUsage(res, usage[res], requested[res], limits)
		fmt.Fprintf(&b, "   %s  %s  %s  %s\n",
			u.resourceLabel(res),
			u.renderUsageBar(res, summary.pct, barWidth),
			lipgloss.NewStyle().Foreground(podsSurfaceText).Bold(true).Render(fmt.Sprintf("%3.0f%%", summary.pct*100)),
			lipgloss.NewStyle().Foreground(podsSurfaceDim).Render(fmt.Sprintf("%s/%s", formatResourceQuantity(res, usage[res]), summary.baselineLabel)),
		)
		if u.showDetails {
			fmt.Fprintf(&b, "      %s  %s\n",
				renderBadge(fmt.Sprintf("req %s", summary.requestedLabel), podsSurfaceMuted, podsSurfaceAltBg, false),
				renderBadge(fmt.Sprintf("lim %s", limitLabelForResource(res, requested[res], limits)), podsSurfaceMuted, podsSurfaceAltBg, false),
			)
		}
	}

	// Comfortable density adds a blank spacing line between pod rows.
	if u.style.Density() == DensityComfortable {
		b.WriteString("\n")
	}

	return b.String()
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

	chips := []struct{ key, label string }{
		{"↑↓", "nav"},
		{"←→", "page"},
		{"enter", "actions"},
		{"c/m/s", "sort"},
		{"g", "group"},
		{"i", "details"},
		{"/", "filter"},
		{"q", "quit"},
	}
	rendered := make([]string, 0, len(chips))
	for _, c := range chips {
		rendered = append(rendered, renderKbdChip(c.key, c.label))
	}
	fmt.Fprintln(w, strings.Join(rendered, "  "))
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
	return tea.Tick(podsTickInterval, func(t time.Time) tea.Msg {
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
		u.pauseAutoSortForKeyboard()
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

	rightWidth := u.rightPaneWidth()
	nodeSection := u.renderNodeUsagePanel(nodes, pods, nodeAliases, rightWidth)
	if strings.TrimSpace(nodeSection) == "" {
		nodeSection = u.renderNodeUsagePlaceholder(rightWidth)
	}
	right := strings.TrimRight(nodeSection+"\n"+u.renderSignalsPanel(nodes, pods, filteredPods, rightWidth), "\n")
	return sideBySideFixed(left, right, u.leftPaneWidth(), rightWidth, 3)
}

func (u *PodsUIModel) renderNodeUsagePanel(nodes []*Node, pods []*Pod, nodeAliases map[string]string, width int) string {
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

	barW := u.nodeSlimBarWidth(width)
	var inner strings.Builder
	for i, row := range rows {
		name := lipgloss.NewStyle().Foreground(podsSurfaceText).Bold(true).Render(row.alias)
		if u.showDetails {
			name = lipgloss.NewStyle().Foreground(podsSurfaceText).Bold(true).Render(fmt.Sprintf("%s (%s)", row.alias, row.name))
		}
		extras := []string{lipgloss.NewStyle().Foreground(podsSurfaceDim).Render(fmt.Sprintf("%d pods", row.pods))}
		if !row.ready {
			extras = append(extras, renderBadge("NotReady", lipgloss.Color(u.style.redHex), lipgloss.Color("#33191E"), true))
		}
		if row.cordoned {
			extras = append(extras, renderBadge("Cordoned", lipgloss.Color(u.style.yellowHex), lipgloss.Color("#332613"), true))
		}
		fmt.Fprintf(&inner, "%s  %s\n", name, strings.Join(extras, " "))

		cpuReqPct := quantityPct(row.cpuAssigned, row.cpuAlloc)
		memReqPct := quantityPct(row.memAssigned, row.memAlloc)
		fmt.Fprintf(&inner, "%s  %s  %s\n",
			u.resourceLabel(v1.ResourceCPU),
			renderSlimBar(row.cpuPct, cpuReqPct, barW, podsCPUColor),
			lipgloss.NewStyle().Foreground(podsSurfaceText).Bold(true).Render(fmt.Sprintf("%3.0f%%", row.cpuPct*100)),
		)
		fmt.Fprintf(&inner, "%s  %s  %s",
			u.resourceLabel(v1.ResourceMemory),
			renderSlimBar(row.memPct, memReqPct, barW, podsMemColor),
			lipgloss.NewStyle().Foreground(podsSurfaceText).Bold(true).Render(fmt.Sprintf("%3.0f%%", row.memPct*100)),
		)
		if i < len(rows)-1 {
			inner.WriteString("\n")
			inner.WriteString(lipgloss.NewStyle().Foreground(podsPanelBorder).Render(strings.Repeat("─", width-4)))
		}
		inner.WriteString("\n")
	}
	return renderPanel(width, "Node Pressure", "", strings.TrimRight(inner.String(), "\n"), podsPanelBorder)
}

// nodeSlimBarWidth sizes the per-node bar so the row fits inside the right
// panel (panel border + padding + CPU/MEM label + pct ≈ 12 chars).
func (u *PodsUIModel) nodeSlimBarWidth(panelWidth int) int {
	w := panelWidth - 14
	if w < 8 {
		w = 8
	}
	if w > 40 {
		w = 40
	}
	return w
}

func (u *PodsUIModel) renderSignalsPanel(_ []*Node, _ []*Pod, filteredPods []*Pod, width int) string {
	if width == 0 {
		return ""
	}

	rows := make([]struct {
		label string
		name  string
		value string
		fg    lipgloss.Color
	}, 0, 4)

	add := func(label, name, value string, fg lipgloss.Color) {
		rows = append(rows, struct {
			label string
			name  string
			value string
			fg    lipgloss.Color
		}{label, name, value, fg})
	}

	if pod, pct := u.topUsagePod(filteredPods, v1.ResourceCPU); pod != nil {
		add("CPU hot", pod.Name(), fmt.Sprintf("%.0f%%", pct*100), podsCPUColor)
	}
	if pod, pct := u.topUsagePod(filteredPods, v1.ResourceMemory); pod != nil {
		add("MEM hot", pod.Name(), fmt.Sprintf("%.0f%%", pct*100), podsMemColor)
	}
	if pod, restarts := topRestartPod(filteredPods); pod != nil && restarts > 0 {
		add("Restarts", pod.Name(), fmt.Sprintf("%dr", restarts), lipgloss.Color(u.style.yellowHex))
	} else {
		add("Restarts", "Stable", "0r", u.style.Accent())
	}
	if pod := firstUnhealthyPod(filteredPods); pod != nil {
		add("Watch", pod.Name(), pod.Health().Label, u.style.SeverityColor(pod.Health().Severity))
	} else {
		add("Watch", "Cluster healthy", "OK", u.style.Accent())
	}

	innerWidth := width - 4
	var inner strings.Builder
	for i, r := range rows {
		label := lipgloss.NewStyle().Foreground(r.fg).Bold(true).Render(fmt.Sprintf("%-9s", strings.ToUpper(r.label)))
		value := lipgloss.NewStyle().Foreground(podsSurfaceText).Bold(true).Render(r.value)
		nameWidth := innerWidth - ansi.StringWidth(label) - ansi.StringWidth(value) - 2
		if nameWidth < 8 {
			nameWidth = 8
		}
		name := lipgloss.NewStyle().Foreground(podsSurfaceMuted).Render(truncateRunes(r.name, nameWidth))
		gap := innerWidth - ansi.StringWidth(label) - ansi.StringWidth(name) - ansi.StringWidth(value) - 1
		if gap < 1 {
			gap = 1
		}
		fmt.Fprintf(&inner, "%s %s%s%s", label, name, strings.Repeat(" ", gap), value)
		if i < len(rows)-1 {
			inner.WriteString("\n")
		}
	}
	return renderPanel(width, "Highlights", "", inner.String(), podsPanelBorder)
}

func (u *PodsUIModel) renderNodeUsagePlaceholder(width int) string {
	body := lipgloss.NewStyle().Foreground(podsSurfaceMuted).Render("Waiting for node data…")
	return renderPanel(width, "Node Pressure", "", body, podsPanelBorder)
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

// renderUsageBar honors u.style.BarStyle() — segmented (default), solid, or
// gradient. The numeric % label is rendered separately by the caller so the
// bar itself is purely visual.
func (u *PodsUIModel) renderUsageBar(resourceName v1.ResourceName, pct float64, width int) string {
	if width < 10 {
		width = 10
	}
	fill, empty, _ := resourceBarPalette(resourceName)
	switch u.style.BarStyle() {
	case BarStyleSolid:
		return renderSolidBar(pct, width, fill, empty)
	case BarStyleGradient:
		return renderGradientBar(pct, width, fill, empty,
			u.style.SeverityColor(PodHealthHealthy),
			u.style.SeverityColor(PodHealthWarning),
			u.style.SeverityColor(PodHealthCritical),
		)
	default:
		return renderSegmentedBar(pct, width, fill, empty)
	}
}

func resourceBarPalette(resourceName v1.ResourceName) (fill lipgloss.Color, empty lipgloss.Color, marker lipgloss.Color) {
	switch resourceName {
	case v1.ResourceCPU:
		return podsCPUColor, podsCPUEmpty, lipgloss.Color("#2A1F18")
	case v1.ResourceMemory:
		return podsMemColor, podsMemEmpty, lipgloss.Color("#1A2634")
	default:
		return podsDefaultColor, lipgloss.Color("#1C2230"), lipgloss.Color("#173124")
	}
}

// podPanelWidth is the width of the bordered pods panel — fills the left pane
// when present, otherwise the full terminal width.
func (u *PodsUIModel) podPanelWidth() int {
	if w := u.leftPaneWidth(); w > 0 {
		return w
	}
	if u.width > 0 {
		return u.width
	}
	return 96
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
	// The bar sits inside the bordered pods panel and after the gutter +
	// resource label, with room on the right for the pct (4 cells) and
	// used/base (~16 cells). Reserve ~30 cells of chrome.
	panelInner := u.podPanelWidth() - 4
	if panelInner <= 0 {
		return 24
	}
	reserved := 30
	if u.showDetails {
		reserved = 48
	}
	width := panelInner - reserved
	if width < 16 {
		width = 16
	}
	if width > 56 {
		width = 56
	}
	return width
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

// headerWidth is the width the header band spans. It crosses both panes when
// the layout is wide enough to show the right column, otherwise it just fills
// the available terminal width.
func (u *PodsUIModel) headerWidth() int {
	if u.width <= 0 {
		return 96
	}
	return u.width
}

// linesPerPod returns the number of terminal lines one pod block occupies.
// - 1 name line
// - N resource-bar lines (one per resource)
// - 1 blank spacing line in comfortable mode
// - 1 group-header line amortised as a fixed overhead in availablePodLines
func (u *PodsUIModel) linesPerPod() int {
	base := 1 + len(u.resources)
	if u.style.Density() == DensityComfortable {
		base++
	}
	return base
}

func (u *PodsUIModel) availablePodLines() int {
	height := u.height
	if height <= 0 {
		height = 32
	}

	footerLines := 2 // paginator dots + kbd chips row
	if strings.TrimSpace(u.filterQuery) != "" || u.filtering {
		footerLines++
	}
	if u.currentStatus() != "" {
		footerLines++
	}

	// 2 for pod-panel top/bottom borders; 1 for the group-header inside the panel.
	const podPanelChrome = 3

	headerLines := renderedLineCount(u.renderHeader(u.cluster.Stats(), u.cluster.VisiblePods(), u.filterPods(u.cluster.VisiblePods())))
	lines := height - headerLines - footerLines - podPanelChrome
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
	u.podOrder = nil
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

func renderMetaField(label string, value string) string {
	return renderMetaFieldWithColor(label, value, podsSurfaceText)
}

func renderMetaFieldWithColor(label string, value string, valueColor lipgloss.TerminalColor) string {
	labelPart := lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B")).Render(label + ":")
	valuePart := lipgloss.NewStyle().Foreground(valueColor).Bold(true).Render(value)
	return labelPart + " " + valuePart
}

func renderBadge(text string, fg lipgloss.TerminalColor, bg lipgloss.TerminalColor, bold bool) string {
	style := lipgloss.NewStyle().Foreground(fg).Background(bg).Padding(0, 1)
	if bold {
		style = style.Bold(true)
	}
	return style.Render(text)
}

// renderPanel wraps body in a rounded soft-bordered panel. Title goes embedded
// in the top border (uppercase, dim). rightLabel sits at the right edge of the
// top border. If title is empty, the top border is a plain unbroken rule.
func renderPanel(width int, title string, rightLabel string, body string, borderFG lipgloss.Color) string {
	if width < 12 {
		width = 12
	}
	border := lipgloss.NewStyle().Foreground(borderFG).Render
	innerWidth := width - 2 // space inside the side borders, including padding cells

	titleSeg := ""
	if title != "" {
		titleSeg = " " + lipgloss.NewStyle().Foreground(podsTitleFG).Bold(true).Render(strings.ToUpper(title)) + " "
	}
	rightSeg := ""
	if rightLabel != "" {
		rightSeg = " " + lipgloss.NewStyle().Foreground(podsSurfaceDim).Render(rightLabel) + " "
	}
	titleW := ansi.StringWidth(titleSeg)
	rightW := ansi.StringWidth(rightSeg)
	// width - 2 corners - title - right
	dash := width - 2 - titleW - rightW
	if dash < 0 {
		dash = 0
	}
	// Split the dashes around title (left of title gets 1, rest goes right of title)
	leftDash := 1
	if titleSeg == "" {
		leftDash = dash / 2
	}
	rightDash := dash - leftDash
	if rightDash < 0 {
		rightDash = 0
	}

	top := border("╭") + border(strings.Repeat("─", leftDash)) + titleSeg + border(strings.Repeat("─", rightDash)) + rightSeg + border("╮")

	bodyLines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	var b strings.Builder
	b.WriteString(top + "\n")
	for _, line := range bodyLines {
		fitted := fitANSIWidth(line, innerWidth-2) // -2 for left/right padding spaces
		padding := (innerWidth - 2) - ansi.StringWidth(fitted)
		if padding < 0 {
			padding = 0
		}
		fmt.Fprintf(&b, "%s %s%s %s\n", border("│"), fitted, strings.Repeat(" ", padding), border("│"))
	}
	b.WriteString(border("╰" + strings.Repeat("─", width-2) + "╯"))
	return b.String()
}


// renderKbdChip renders a `kbd`-style chip: a small bordered key followed by a
// dim label. Used in the footer shortcut row.
func renderKbdChip(key string, label string) string {
	keyChip := lipgloss.NewStyle().
		Foreground(podsSurfaceText).
		Background(podsPanelBg).
		Padding(0, 1).
		Render(key)
	labelText := lipgloss.NewStyle().Foreground(podsTitleFG).Render(label)
	return keyChip + " " + labelText
}

// renderSegmentedBar draws a solid filled bar using background-colored spaces.
// Each cell is exactly 1 column wide, which avoids terminal font double-width
// rendering bugs that cause line wrapping and bubbletea redraw artifacts.
// pct is 0..1.
func renderSegmentedBar(pct float64, width int, fill lipgloss.Color, empty lipgloss.Color) string {
	if width < 6 {
		width = 6
	}
	bounded := boundPct(pct)
	filled := int(math.Round(bounded * float64(width)))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	cells := make([]string, width)
	for i := 0; i < width; i++ {
		bg := empty
		if i < filled {
			bg = fill
		}
		cells[i] = lipgloss.NewStyle().Background(bg).Render(" ")
	}
	return strings.Join(cells, "")
}

// renderSolidBar is identical to renderSegmentedBar — both now use background
// fills. Kept as a named function so --bar-style=solid remains a valid flag.
func renderSolidBar(pct float64, width int, fill lipgloss.Color, empty lipgloss.Color) string {
	return renderSegmentedBar(pct, width, fill, empty)
}

// renderGradientBar draws a filled bar where the fill color shifts from red
// through yellow to the accent green based on position relative to thresholds.
func renderGradientBar(pct float64, width int, _fill, empty lipgloss.Color, lowFG, midFG, highFG lipgloss.Color) string {
	if width < 6 {
		width = 6
	}
	bounded := boundPct(pct)
	filled := int(math.Round(bounded * float64(width)))
	cells := make([]string, width)
	warnAt := int(math.Round(0.7 * float64(width)))
	critAt := int(math.Round(0.9 * float64(width)))
	for i := 0; i < width; i++ {
		bg := empty
		if i < filled {
			switch {
			case i >= critAt:
				bg = highFG
			case i >= warnAt:
				bg = midFG
			default:
				bg = lowFG
			}
		}
		cells[i] = lipgloss.NewStyle().Background(bg).Render(" ")
	}
	return strings.Join(cells, "")
}

// renderSlimBar draws a filled bar with a request-marker tick. Used in the
// node pressure panel. Uses background-colored spaces to guarantee single-cell
// width on all terminal fonts.
func renderSlimBar(pct float64, reqPct float64, width int, fill lipgloss.Color) string {
	if width < 6 {
		width = 6
	}
	bounded := boundPct(pct)
	filled := int(math.Round(bounded * float64(width)))
	marker := int(math.Round(boundPct(reqPct) * float64(width)))
	if marker >= width {
		marker = width - 1
	}
	if marker < 0 {
		marker = 0
	}
	cells := make([]string, width)
	for i := 0; i < width; i++ {
		switch {
		case i == marker && reqPct > 0:
			cells[i] = lipgloss.NewStyle().Foreground(podsSurfaceText).Bold(true).Render("|")
		case i < filled:
			cells[i] = lipgloss.NewStyle().Background(fill).Render(" ")
		default:
			cells[i] = lipgloss.NewStyle().Background(podsPanelBg).Foreground(podsSurfaceDim).Render("-")
		}
	}
	return strings.Join(cells, "")
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

// overlayAt paints overlay on top of background starting at (startRow, startCol)
// while preserving the background content to the RIGHT of the overlay panel.
// This keeps the sidebar (node pressure + highlights) visible even when the
// popup overlays the pod list on the left side.
//
// For each overlay row:
//   left  = background[:startCol]   (ANSI-truncated)
//   mid   = overlay line            (the popup panel line)
//   right = background[startCol+overlayWidth:]  (ANSI-skipped, keeps sidebar)
func overlayAt(background, overlay string, startRow, startCol int) string {
	// Preserve whatever trailing newline the background had so the footer
	// separator is not broken when we reassemble the lines.
	trailingNL := strings.HasSuffix(background, "\n")

	bgLines := strings.Split(strings.TrimRight(background, "\n"), "\n")
	ovLines := strings.Split(strings.TrimRight(overlay, "\n"), "\n")

	// Clip overlay to background height — never add new lines.
	if maxOv := len(bgLines) - startRow; maxOv < len(ovLines) {
		if maxOv <= 0 {
			return background
		}
		ovLines = ovLines[:maxOv]
	}

	for i, ovLine := range ovLines {
		r := startRow + i
		bg := bgLines[r]

		// Left: bg up to startCol visible chars.
		left := ansi.Truncate(bg, startCol, "")
		leftW := ansi.StringWidth(left)
		if leftW < startCol {
			left += strings.Repeat(" ", startCol-leftW)
		}

		// Right: bg content after (startCol + overlay visible width).
		ovW := ansi.StringWidth(ovLine)
		right := ansi.TruncateLeft(bg, startCol+ovW, "")

		bgLines[r] = left + ovLine + right
	}
	result := strings.Join(bgLines, "\n")
	if trailingNL {
		result += "\n"
	}
	return result
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
