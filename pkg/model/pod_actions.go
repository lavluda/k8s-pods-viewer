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
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type podActionKind string

const (
	podActionExec      podActionKind = "exec"
	podActionLogs      podActionKind = "logs"
	podActionDescribe  podActionKind = "describe"
	podActionKill      podActionKind = "kill"
	podActionScaleDown podActionKind = "scale_down"
	podActionScaleUp   podActionKind = "scale_up"
)

var podActionOptions = []podActionKind{
	podActionExec,
	podActionLogs,
	podActionDescribe,
	podActionKill,
}

type podActionOutputMsg struct {
	title string
	body  string
	err   error
}

type podExecFinishedMsg struct {
	err error
}

type podMutationMsg struct {
	status string
	err    error
}

type podListState struct {
	visiblePods   []*Pod
	filteredPods  []*Pod
	renderedPods  []*Pod
	nodeAliases   map[string]string
	pages         [][]podGroup
	pageStarts    []int
	selectedIndex int
	selectedPod   *Pod
}

func (a podActionKind) label() string {
	switch a {
	case podActionExec:
		return "Exec"
	case podActionLogs:
		return "Logs"
	case podActionDescribe:
		return "Describe"
	case podActionKill:
		return "Kill Pod"
	case podActionScaleDown:
		return "Scale -1"
	case podActionScaleUp:
		return "Scale +1"
	default:
		return string(a)
	}
}

func podActionOptionsForPod(pod *Pod) []podActionKind {
	options := append([]podActionKind(nil), podActionOptions...)
	if scalableWorkloadRef(pod) != "" {
		options = append(options, podActionScaleDown, podActionScaleUp)
	}
	return options
}

func displayedPodActionOptions(pod *Pod) []podActionKind {
	options := []podActionKind{
		podActionExec,
		podActionLogs,
		podActionDescribe,
	}
	if scalableWorkloadRef(pod) != "" {
		options = append(options, podActionScaleDown, podActionScaleUp)
	}
	return append(options, podActionKill)
}

func isDangerousAction(action podActionKind) bool {
	switch action {
	case podActionKill, podActionScaleDown, podActionScaleUp:
		return true
	default:
		return false
	}
}

func (u *PodsUIModel) SetKubectlConfig(kubeconfig string, kubeContext string) {
	u.kubeconfig = strings.TrimSpace(kubeconfig)
	u.kubeContext = strings.TrimSpace(kubeContext)
}

func (u *PodsUIModel) buildPodListState() podListState {
	state := podListState{}
	state.visiblePods = u.cluster.VisiblePods()
	sort.Slice(state.visiblePods, func(a, b int) bool {
		return u.podSorter(state.visiblePods[a], state.visiblePods[b])
	})
	state.filteredPods = u.filterPods(state.visiblePods)
	state.nodeAliases = buildNodeAliases(u.cluster.Stats().Nodes, state.visiblePods)
	groups := u.groupPods(state.filteredPods)
	state.renderedPods = flattenGroupedPods(groups)
	state.pages = u.paginateGroups(groups)
	state.pageStarts = make([]int, len(state.pages))

	total := 0
	for index, page := range state.pages {
		state.pageStarts[index] = total
		total += pagePodCount(page)
	}

	state.selectedIndex = u.ensureSelectedPod(state.renderedPods)
	if len(state.renderedPods) > 0 && !u.selectionPinned {
		state.selectedIndex = 0
		u.setSelectedPod(state.renderedPods[0])
	}
	if state.selectedIndex >= 0 && state.selectedIndex < len(state.renderedPods) {
		state.selectedPod = state.renderedPods[state.selectedIndex]
	}
	u.syncPaginatorWithSelection(state)
	return state
}

func pagePodCount(page []podGroup) int {
	total := 0
	for _, group := range page {
		total += len(group.pods)
	}
	return total
}

func (u *PodsUIModel) ensureSelectedPod(pods []*Pod) int {
	if len(pods) == 0 {
		u.hasSelectedPod = false
		u.selectedPod = objectKey{}
		u.selectionPinned = false
		u.actionMenuOpen = false
		u.containerMenuOpen = false
		return -1
	}

	if u.hasSelectedPod {
		for index, pod := range pods {
			if pod.Namespace() == u.selectedPod.namespace && pod.Name() == u.selectedPod.name {
				return index
			}
		}
	}

	u.setSelectedPod(pods[0])
	return 0
}

func (u *PodsUIModel) setSelectedPod(pod *Pod) {
	if pod == nil {
		u.hasSelectedPod = false
		u.selectedPod = objectKey{}
		return
	}
	u.selectedPod = objectKey{namespace: pod.Namespace(), name: pod.Name()}
	u.hasSelectedPod = true
}

func (u *PodsUIModel) pageForSelection(state podListState) int {
	if len(state.pages) == 0 || state.selectedIndex < 0 {
		return 0
	}
	for index, start := range state.pageStarts {
		end := start + pagePodCount(state.pages[index])
		if state.selectedIndex >= start && state.selectedIndex < end {
			return index
		}
	}
	return 0
}

func (u *PodsUIModel) syncPaginatorWithSelection(state podListState) {
	u.paginator.PerPage = 1
	u.paginator.SetTotalPages(maxInt(1, len(state.pages)))
	page := u.pageForSelection(state)
	if page >= u.paginator.TotalPages {
		page = u.paginator.TotalPages - 1
	}
	if page < 0 {
		page = 0
	}
	u.paginator.Page = page
}

func (u *PodsUIModel) selectPodByOffset(offset int) {
	state := u.buildPodListState()
	if len(state.renderedPods) == 0 {
		u.SetTransientStatus("No pods available to select.", 2*time.Second)
		return
	}

	next := state.selectedIndex + offset
	if next < 0 {
		next = 0
	}
	if next >= len(state.renderedPods) {
		next = len(state.renderedPods) - 1
	}
	u.selectionPinned = true
	u.setSelectedPod(state.renderedPods[next])
}

func (u *PodsUIModel) selectPage(page int) bool {
	state := u.buildPodListState()
	if len(state.pages) == 0 {
		return false
	}
	if page < 0 {
		page = 0
	}
	if page >= len(state.pages) {
		page = len(state.pages) - 1
	}

	pagePods := flattenPagePods(state.pages[page])
	if len(pagePods) == 0 {
		return false
	}
	u.selectionPinned = true
	u.setSelectedPod(pagePods[0])
	return true
}

func flattenPagePods(page []podGroup) []*Pod {
	pods := make([]*Pod, 0, pagePodCount(page))
	for _, group := range page {
		pods = append(pods, group.pods...)
	}
	return pods
}

func flattenGroupedPods(groups []podGroup) []*Pod {
	pods := make([]*Pod, 0)
	for _, group := range groups {
		pods = append(pods, group.pods...)
	}
	return pods
}

func (u *PodsUIModel) openActionMenu() {
	if u.buildPodListState().selectedPod == nil {
		u.SetTransientStatus("No pods available to open.", 2*time.Second)
		return
	}
	u.selectionPinned = true
	u.actionMenuOpen = true
	u.confirmActionOpen = false
	u.containerMenuOpen = false
	u.actionMenuIndex = 0
}

func (u *PodsUIModel) resetSelectionAnchor() {
	u.selectionPinned = false
}

func (u *PodsUIModel) closeMenus() {
	u.actionMenuOpen = false
	u.confirmActionOpen = false
	u.confirmActionIndex = 0
	u.containerMenuOpen = false
}

func (u *PodsUIModel) closeViewer() {
	u.viewerOpen = false
	u.viewerLoading = false
	u.viewerTitle = ""
	u.viewerBody = ""
	u.viewerScroll = 0
}

func (u *PodsUIModel) openViewer(title string) {
	u.viewerOpen = true
	u.viewerLoading = true
	u.viewerTitle = title
	u.viewerBody = ""
	u.viewerScroll = 0
}

func (u *PodsUIModel) handleViewerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		u.closeViewer()
		return u, nil
	case "up", "k":
		if u.viewerScroll > 0 {
			u.viewerScroll--
		}
		return u, nil
	case "down", "j":
		maxScroll := maxInt(0, len(u.viewerLines())-u.viewerBodyHeight())
		if u.viewerScroll < maxScroll {
			u.viewerScroll++
		}
		return u, nil
	case "pgup", "b":
		u.viewerScroll -= u.viewerBodyHeight()
		if u.viewerScroll < 0 {
			u.viewerScroll = 0
		}
		return u, nil
	case "pgdown", "f":
		u.viewerScroll += u.viewerBodyHeight()
		maxScroll := maxInt(0, len(u.viewerLines())-u.viewerBodyHeight())
		if u.viewerScroll > maxScroll {
			u.viewerScroll = maxScroll
		}
		return u, nil
	case "g":
		u.viewerScroll = 0
		return u, nil
	case "G":
		u.viewerScroll = maxInt(0, len(u.viewerLines())-u.viewerBodyHeight())
		return u, nil
	}
	return u, nil
}

func (u *PodsUIModel) handleActionMenuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	state := u.buildPodListState()
	pod := state.selectedPod
	if pod == nil {
		u.closeMenus()
		return u, nil
	}
	options := displayedPodActionOptions(pod)

	switch msg.String() {
	case "esc":
		u.closeMenus()
		return u, nil
	case "up", "k":
		if u.actionMenuIndex > 0 {
			u.actionMenuIndex--
		}
		return u, nil
	case "down", "j":
		if u.actionMenuIndex < len(options)-1 {
			u.actionMenuIndex++
		}
		return u, nil
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		index := int(msg.String()[0] - '1')
		if index >= 0 && index < len(options) {
			u.actionMenuIndex = index
			return u, u.resolvePodAction(options[index])
		}
		return u, nil
	case "enter":
		if u.actionMenuIndex >= 0 && u.actionMenuIndex < len(options) {
			return u, u.resolvePodAction(options[u.actionMenuIndex])
		}
		return u, nil
	}
	return u, nil
}

func (u *PodsUIModel) handleConfirmActionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	state := u.buildPodListState()
	pod := state.selectedPod
	if pod == nil {
		u.closeMenus()
		return u, nil
	}

	switch msg.String() {
	case "esc":
		u.confirmActionOpen = false
		u.confirmActionIndex = 0
		u.actionMenuOpen = true
		return u, nil
	case "left", "h", "up", "k", "shift+tab":
		u.confirmActionIndex = 0
		return u, nil
	case "right", "l", "down", "j", "tab":
		u.confirmActionIndex = 1
		return u, nil
	case "enter":
		if u.confirmActionIndex == 1 {
			u.confirmActionOpen = false
			u.confirmActionIndex = 0
			u.actionMenuOpen = true
			return u, nil
		}
		action := u.confirmAction
		u.confirmActionOpen = false
		u.confirmActionIndex = 0
		return u, u.startPodAction(action, pod, "")
	}
	return u, nil
}

func (u *PodsUIModel) handleContainerMenuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	state := u.buildPodListState()
	pod := state.selectedPod
	if pod == nil {
		u.closeMenus()
		return u, nil
	}

	containers := pod.Containers()
	switch msg.String() {
	case "esc":
		u.containerMenuOpen = false
		u.actionMenuOpen = true
		return u, nil
	case "up", "k":
		if u.containerMenuIndex > 0 {
			u.containerMenuIndex--
		}
		return u, nil
	case "down", "j":
		if u.containerMenuIndex < len(containers)-1 {
			u.containerMenuIndex++
		}
		return u, nil
	case "enter":
		if u.containerMenuIndex >= 0 && u.containerMenuIndex < len(containers) {
			return u, u.startPodAction(u.pendingAction, pod, containers[u.containerMenuIndex])
		}
		return u, nil
	}
	return u, nil
}

func (u *PodsUIModel) resolvePodAction(action podActionKind) tea.Cmd {
	state := u.buildPodListState()
	pod := state.selectedPod
	if pod == nil {
		u.closeMenus()
		return nil
	}

	switch action {
	case podActionDescribe:
		return u.startPodAction(action, pod, "")
	case podActionExec, podActionLogs:
		containers := pod.Containers()
		if len(containers) == 0 {
			u.closeMenus()
			u.SetTransientStatus("Selected pod has no regular containers.", 3*time.Second)
			return nil
		}
		if len(containers) == 1 {
			return u.startPodAction(action, pod, containers[0])
		}

		u.pendingAction = action
		u.containerMenuIndex = 0
		u.containerMenuOpen = true
		u.actionMenuOpen = false
		return nil
	case podActionKill, podActionScaleDown, podActionScaleUp:
		u.confirmAction = action
		u.confirmActionOpen = true
		u.confirmActionIndex = 0
		u.actionMenuOpen = false
		return nil
	default:
		return nil
	}
}

func (u *PodsUIModel) startPodAction(action podActionKind, pod *Pod, container string) tea.Cmd {
	u.closeMenus()

	switch action {
	case podActionExec:
		return u.execKubectlCmd(pod, container)
	case podActionLogs:
		title := fmt.Sprintf("Logs: %s", pod.FullName())
		if container != "" {
			title = fmt.Sprintf("%s [%s]", title, container)
		}
		u.openViewer(title)
		args := u.kubectlArgs("logs", "-n", pod.Namespace(), pod.Name(), "--tail=200", "--timestamps")
		args = append(args, containerFlag(container)...)
		return u.fetchKubectlOutputCmd(title, args)
	case podActionDescribe:
		title := fmt.Sprintf("Describe: %s", pod.FullName())
		u.openViewer(title)
		return u.fetchKubectlOutputCmd(title, u.kubectlArgs("describe", "pod", "-n", pod.Namespace(), pod.Name()))
	case podActionKill:
		return u.mutateKubectlCmd(
			fmt.Sprintf("Killed pod %s.", pod.FullName()),
			"kill pod",
			u.kubectlArgs("delete", "pod", "-n", pod.Namespace(), pod.Name(), "--wait=false"),
		)
	case podActionScaleDown:
		return u.scaleWorkloadCmd(pod, -1)
	case podActionScaleUp:
		return u.scaleWorkloadCmd(pod, 1)
	default:
		return nil
	}
}

func (u *PodsUIModel) kubectlArgs(args ...string) []string {
	prefix := []string{}
	if u.kubeconfig != "" {
		prefix = append(prefix, "--kubeconfig", u.kubeconfig)
	}
	if u.kubeContext != "" {
		prefix = append(prefix, "--context", u.kubeContext)
	}
	return append(prefix, args...)
}

func containerFlag(container string) []string {
	if strings.TrimSpace(container) == "" {
		return nil
	}
	return []string{"-c", container}
}

func (u *PodsUIModel) fetchKubectlOutputCmd(title string, args []string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("kubectl", args...)
		output, err := cmd.CombinedOutput()
		body := strings.TrimRight(string(output), "\n")
		if body == "" && err == nil {
			body = "No output."
		}
		return podActionOutputMsg{
			title: title,
			body:  body,
			err:   err,
		}
	}
}

func (u *PodsUIModel) execKubectlCmd(pod *Pod, container string) tea.Cmd {
	args := u.kubectlArgs("exec", "-it", "-n", pod.Namespace(), pod.Name())
	args = append(args, containerFlag(container)...)
	args = append(args, "--", "sh")

	cmd := exec.Command("kubectl", args...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return podExecFinishedMsg{err: err}
	})
}

func (u *PodsUIModel) mutateKubectlCmd(successStatus string, actionLabel string, args []string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("kubectl", args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			body := strings.TrimSpace(string(output))
			if body == "" {
				body = err.Error()
			}
			return podMutationMsg{err: fmt.Errorf("%s failed: %s", actionLabel, body)}
		}
		return podMutationMsg{status: successStatus}
	}
}

func (u *PodsUIModel) scaleWorkloadCmd(pod *Pod, delta int) tea.Cmd {
	workloadRef := scalableWorkloadRef(pod)
	if workloadRef == "" {
		return func() tea.Msg {
			return podMutationMsg{err: fmt.Errorf("selected pod does not belong to a scalable workload")}
		}
	}

	kind, workloadName := pod.Workload()
	namespace := pod.Namespace()
	return func() tea.Msg {
		getArgs := u.kubectlArgs("get", workloadRef, "-n", namespace, "-o", "jsonpath={.spec.replicas}")
		getCmd := exec.Command("kubectl", getArgs...)
		currentOutput, err := getCmd.CombinedOutput()
		if err != nil {
			body := strings.TrimSpace(string(currentOutput))
			if body == "" {
				body = err.Error()
			}
			return podMutationMsg{err: fmt.Errorf("scale lookup failed: %s", body)}
		}

		currentReplicas := 1
		currentText := strings.TrimSpace(string(currentOutput))
		if currentText != "" {
			parsed, parseErr := strconv.Atoi(currentText)
			if parseErr != nil {
				return podMutationMsg{err: fmt.Errorf("scale lookup returned invalid replicas %q", currentText)}
			}
			currentReplicas = parsed
		}

		targetReplicas := currentReplicas + delta
		if targetReplicas < 0 {
			targetReplicas = 0
		}

		scaleArgs := u.kubectlArgs("scale", workloadRef, "-n", namespace, fmt.Sprintf("--replicas=%d", targetReplicas))
		scaleCmd := exec.Command("kubectl", scaleArgs...)
		scaleOutput, err := scaleCmd.CombinedOutput()
		if err != nil {
			body := strings.TrimSpace(string(scaleOutput))
			if body == "" {
				body = err.Error()
			}
			return podMutationMsg{err: fmt.Errorf("scale failed: %s", body)}
		}

		return podMutationMsg{
			status: fmt.Sprintf("Scaled %s %s to %d.", strings.ToLower(kind), workloadName, targetReplicas),
		}
	}
}

func scalableWorkloadRef(pod *Pod) string {
	kind, name := pod.Workload()
	switch strings.ToLower(kind) {
	case "deployment":
		return "deployment/" + name
	case "statefulset":
		return "statefulset/" + name
	case "replicaset":
		return "replicaset/" + name
	case "replicationcontroller":
		return "replicationcontroller/" + name
	default:
		return ""
	}
}

func (u *PodsUIModel) renderActionOverlay(state podListState) string {
	if u.viewerOpen {
		return u.renderViewer()
	}
	return ""
}

func (u *PodsUIModel) renderActionPopover(pod *Pod) string {
	options := displayedPodActionOptions(pod)
	lines := []string{
		lipgloss.NewStyle().Foreground(podsSurfaceMuted).Render("Selected pod"),
		lipgloss.NewStyle().Foreground(podsSurfaceText).Bold(true).Render(truncateRunes(pod.FullName(), 30)),
		"",
	}

	title := "Pod Actions"
	if u.confirmActionOpen {
		title = "Confirm Action"
		lines = append(lines, u.renderConfirmMessage(pod)...)
		lines = append(lines, "")
		lines = append(lines, u.renderConfirmButtons())
	} else if u.containerMenuOpen {
		title = fmt.Sprintf("Container For %s", u.pendingAction.label())
		for index, container := range pod.Containers() {
			lines = append(lines, u.renderPopoverItem(container, index == u.containerMenuIndex, ""))
		}
		lines = append(lines, "")
		lines = append(lines, podsHelpStyle("enter choose • esc back"))
	} else {
		inspect := filterPodActions(options, func(action podActionKind) bool {
			return action == podActionExec || action == podActionLogs || action == podActionDescribe
		})
		lines = append(lines, u.renderActionSection("Inspect", inspect, 0, false)...)
		if scalableWorkloadRef(pod) != "" {
			scale := filterPodActions(options, func(action podActionKind) bool {
				return action == podActionScaleDown || action == podActionScaleUp
			})
			lines = append(lines, "")
			lines = append(lines, u.renderActionSection("Scale", scale, len(inspect), false)...)
		}
		danger := filterPodActions(options, func(action podActionKind) bool {
			return action == podActionKill
		})
		lines = append(lines, "")
		lines = append(lines, u.renderActionSection("Danger", danger, len(options)-len(danger), true)...)
		lines = append(lines, "")
		lines = append(lines, podsHelpStyle("enter choose • esc close"))
	}

	width := 34
	if leftWidth := u.leftPaneWidth(); leftWidth > 0 {
		width = minInt(38, maxInt(30, leftWidth/3))
	}

	titleBar := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#DDEAFE")).
		Background(lipgloss.Color("#14213D")).
		Bold(true).
		Padding(0, 1).
		Render(title)

	content := strings.Join(lines, "\n")
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#475569")).
		Background(lipgloss.Color("#0F172A")).
		Padding(0, 1).
		Width(width).
		Render(titleBar + "\n" + content)

	shadow := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#020617")).
		Render("░")
	boxLines := strings.Split(box, "\n")
	for index, line := range boxLines {
		if index == 0 {
			continue
		}
		boxLines[index] = line + shadow
	}
	boxLines = append(boxLines, lipgloss.NewStyle().Foreground(lipgloss.Color("#020617")).Render(strings.Repeat("░", maxInt(0, lipgloss.Width(boxLines[0])-1))))
	return strings.Join(boxLines, "\n")
}

func filterPodActions(options []podActionKind, include func(action podActionKind) bool) []podActionKind {
	filtered := make([]podActionKind, 0, len(options))
	for _, action := range options {
		if include(action) {
			filtered = append(filtered, action)
		}
	}
	return filtered
}

func (u *PodsUIModel) renderActionSection(title string, options []podActionKind, offset int, danger bool) []string {
	lines := []string{
		u.renderPopoverSectionHeader(title, danger),
	}
	for index, action := range options {
		hint := strconv.Itoa(offset + index + 1)
		lines = append(lines, u.renderPopoverActionItem(action, offset+index == u.actionMenuIndex, hint))
	}
	return lines
}

func (u *PodsUIModel) renderConfirmMessage(pod *Pod) []string {
	action := u.confirmAction
	switch action {
	case podActionKill:
		return []string{
			u.renderPopoverSectionHeader("Danger", true),
			lipgloss.NewStyle().Foreground(lipgloss.Color("#FCA5A5")).Bold(true).Render("Delete this pod now?"),
			podsMutedStyle("Kubernetes will recreate it if controlled by a workload."),
		}
	case podActionScaleDown, podActionScaleUp:
		kind, name := pod.Workload()
		verb := "increase"
		if action == podActionScaleDown {
			verb = "decrease"
		}
		return []string{
			u.renderPopoverSectionHeader("Scale", false),
			lipgloss.NewStyle().Foreground(lipgloss.Color("#BFDBFE")).Bold(true).Render(fmt.Sprintf("%s %s %s by 1 replica?", verb, strings.ToLower(kind), name)),
			podsMutedStyle("This changes the owning workload replica count."),
		}
	default:
		return []string{
			podsMutedStyle("Confirm action?"),
		}
	}
}

func (u *PodsUIModel) renderConfirmButtons() string {
	confirm := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#F8FAFC")).
		Background(lipgloss.Color("#7F1D1D")).
		Bold(true).
		Padding(0, 1)
	if u.confirmActionIndex == 0 {
		confirm = confirm.Background(lipgloss.Color("#DC2626"))
	}

	cancel := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#CBD5E1")).
		Background(lipgloss.Color("#1E293B")).
		Bold(true).
		Padding(0, 1)
	if u.confirmActionIndex == 1 {
		cancel = cancel.
			Foreground(lipgloss.Color("#F8FAFC")).
			Background(lipgloss.Color("#334155"))
	}

	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		confirm.Render("enter Confirm"),
		"  ",
		cancel.Render("esc Cancel"),
	)
}

func (u *PodsUIModel) renderPopoverSectionHeader(title string, danger bool) string {
	fg := lipgloss.Color("#CBD5E1")
	bg := lipgloss.Color("#172033")
	if danger {
		fg = lipgloss.Color("#FCA5A5")
		bg = lipgloss.Color("#31111A")
	}
	return lipgloss.NewStyle().
		Foreground(fg).
		Background(bg).
		Bold(true).
		Padding(0, 1).
		Render(strings.ToUpper(title))
}

func (u *PodsUIModel) renderPopoverActionItem(action podActionKind, selected bool, hint string) string {
	iconFG := lipgloss.Color("#93C5FD")
	iconBG := lipgloss.Color("#111827")
	switch action {
	case podActionKill:
		iconFG = lipgloss.Color("#FCA5A5")
		iconBG = lipgloss.Color("#31111A")
	case podActionScaleDown, podActionScaleUp:
		iconFG = lipgloss.Color("#BFDBFE")
		iconBG = lipgloss.Color("#14213D")
	}
	label := fmt.Sprintf("%s  %s", renderBadge(hint, iconFG, iconBG, true), action.label())
	return u.renderPopoverItem(label, selected, "")
}

func (u *PodsUIModel) renderPopoverItem(label string, selected bool, hint string) string {
	prefix := "  "
	rowStyle := lipgloss.NewStyle().
		Foreground(podsSurfaceText).
		Padding(0, 1)
	if selected {
		prefix = "› "
		rowStyle = rowStyle.
			Foreground(lipgloss.Color("#F8FAFC")).
			Background(lipgloss.Color("#1E3A8A")).
			Bold(true)
	}

	if hint != "" {
		label = fmt.Sprintf("%s%s%s", renderBadge(hint, lipgloss.Color("#93C5FD"), lipgloss.Color("#111827"), true), "  ", label)
	}
	return rowStyle.Render(prefix + label)
}

func (u *PodsUIModel) renderViewer() string {
	width := u.width
	if width <= 0 {
		width = 100
	}

	bodyWidth := width - 4
	if bodyWidth < 24 {
		bodyWidth = 24
	}

	lines := u.viewerLines()
	bodyHeight := u.viewerBodyHeight()
	maxScroll := maxInt(0, len(lines)-bodyHeight)
	if u.viewerScroll > maxScroll {
		u.viewerScroll = maxScroll
	}
	if u.viewerScroll < 0 {
		u.viewerScroll = 0
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s\n", renderBadge("POD VIEWER", lipgloss.Color("#0F172A"), lipgloss.Color("#93C5FD"), true), renderMetaField("Title", u.viewerTitle))
	if u.viewerLoading {
		fmt.Fprintln(&b, renderBadge("Loading...", podsSurfaceText, podsSurfaceAltBg, false))
	} else {
		end := minInt(len(lines), u.viewerScroll+bodyHeight)
		for _, line := range lines[u.viewerScroll:end] {
			fmt.Fprintln(&b, fitANSIWidth(line, bodyWidth))
		}
	}
	fmt.Fprintln(&b, renderSectionDividerForViewer(width))
	fmt.Fprintf(&b, "%s\n", podsHelpStyle("scroll ↑/↓ pgup/pgdn • top g • bottom G • back esc"))
	return strings.TrimRight(b.String(), "\n")
}

func renderSectionDividerForViewer(width int) string {
	width -= 2
	if width < 18 {
		width = 18
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#272A36")).Render(strings.Repeat("━", width))
}

func (u *PodsUIModel) viewerLines() []string {
	if u.viewerLoading {
		return []string{"Loading..."}
	}

	body := strings.ReplaceAll(u.viewerBody, "\r\n", "\n")
	if strings.TrimSpace(body) == "" {
		body = "No output."
	}
	lines := strings.Split(body, "\n")
	if len(lines) == 0 {
		return []string{"No output."}
	}
	return lines
}

func (u *PodsUIModel) viewerBodyHeight() int {
	if u.height <= 0 {
		return 18
	}
	height := u.height - 4
	if height < 6 {
		height = 6
	}
	return height
}
