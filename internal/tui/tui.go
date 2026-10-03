package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jrdn/tt/internal/task"
)

type view int

const (
	viewList   view = iota
	viewDetail view = iota
	viewGraph  view = iota
)

// msgs

type tasksLoadedMsg struct{ tasks []task.Task }
type taskDetailMsg struct{ result showResult }
type errMsg struct{ err error }
type actionDoneMsg struct{ msg string }

// Live refresh: a change happened elsewhere, and the reloaded data.
type liveMsg struct{}
type liveTasksMsg struct{ tasks []task.Task }
type liveDetailMsg struct{ result showResult }

// input modes
type inputMode int

const (
	inputNone    inputMode = iota
	inputComment inputMode = iota
	inputCreate  inputMode = iota
	inputStatus  inputMode = iota
)

type Model struct {
	store       task.Store
	view        view
	list        listModel
	detail      detailModel
	graph       graphModel
	tasks       []task.Task
	width       int
	height      int
	statusMsg   string
	errMsg      string
	includeDone bool

	// input state
	inputMode   inputMode
	inputPrompt string
	inputValue  string
	inputTaskID string // task being acted on
	inputStep   int    // for multi-step inputs (create: 0=title)

	// for status picker
	statusChoices []string
	statusIdx     int

	showHelp bool

	// live delivers change events when the store supports them.
	live <-chan task.Event
}

func NewModel(store task.Store) Model {
	m := Model{
		store:         store,
		view:          viewList,
		statusChoices: []string{"open", "backlog", "ready", "in_progress", "in_review", "done", "cancelled"},
	}
	if sub, ok := store.(task.Subscriber); ok {
		// Best effort: without it the TUI still refreshes after its own actions.
		m.live, _ = sub.Subscribe(context.Background())
	}
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.loadTasks(), m.waitForLive())
}

// waitForLive blocks until the next change event, folding any burst of
// queued events into one refresh.
func (m Model) waitForLive() tea.Cmd {
	if m.live == nil {
		return nil
	}
	return func() tea.Msg {
		if _, ok := <-m.live; !ok {
			return nil
		}
		for {
			select {
			case _, ok := <-m.live:
				if !ok {
					return liveMsg{}
				}
			default:
				return liveMsg{}
			}
		}
	}
}

// applyLiveTasks swaps in fresh tasks while keeping the selection on the
// same task, even if rows moved.
func (m *Model) applyLiveTasks(tasks []task.Task) {
	var keep string
	if t := m.list.taskAtCursor(); t != nil {
		keep = t.ID
	}
	m.tasks = tasks
	filtered := m.filteredTasks()
	m.list.rebuildWithTasks(filtered)
	m.graph.rebuild(filtered)
	for i, r := range m.list.rows {
		if keep != "" && r.task != nil && r.task.ID == keep {
			m.list.cursor = i
			break
		}
	}
	if m.list.cursor >= len(m.list.rows) {
		m.list.cursor = max(0, len(m.list.rows)-1)
	}
	m.list.clampOffset()
}

func (m Model) loadTasks() tea.Cmd {
	return func() tea.Msg {
		opts := task.ListOpts{All: true}
		tasks, err := m.store.List(context.Background(), opts)
		if err != nil {
			return errMsg{err}
		}
		return tasksLoadedMsg{tasks}
	}
}

func (m Model) loadDetail(id string) tea.Cmd {
	return func() tea.Msg {
		t, err := m.store.Get(context.Background(), id)
		if err != nil {
			return errMsg{err}
		}
		subtasks, _ := m.store.List(context.Background(), task.ListOpts{ParentID: id, All: true})
		relations, _ := m.store.GetRelations(context.Background(), t.ID)
		comments, _ := m.store.GetComments(context.Background(), t.ID)
		return taskDetailMsg{showResult{
			Task:      *t,
			Subtasks:  subtasks,
			Relations: relations,
			Comments:  comments,
		}}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.relayout()
		return m, nil

	case tasksLoadedMsg:
		m.tasks = msg.tasks
		filtered := m.filteredTasks()
		m.list = newListModel(filtered, m.width, m.listHeight(), m.includeDone)
		m.graph = newGraphModel(filtered, m.width, m.listHeight())
		m.statusMsg = ""
		return m, nil

	case taskDetailMsg:
		m.detail.setResult(&msg.result)
		m.view = viewDetail
		return m, nil

	case errMsg:
		m.errMsg = msg.err.Error()
		return m, nil

	case actionDoneMsg:
		m.statusMsg = msg.msg
		return m, m.loadTasks()

	case liveMsg:
		cmds := []tea.Cmd{m.waitForLive(), m.reloadLive(m.loadTasks(), func(r tea.Msg) tea.Msg {
			if t, ok := r.(tasksLoadedMsg); ok {
				return liveTasksMsg{t.tasks}
			}
			return r
		})}
		// Don't swap the detail out from under an open prompt.
		if m.view == viewDetail && m.detail.result != nil && m.inputMode == inputNone {
			cmds = append(cmds, m.reloadLive(m.loadDetail(m.detail.result.Task.ID), func(r tea.Msg) tea.Msg {
				if d, ok := r.(taskDetailMsg); ok {
					return liveDetailMsg{d.result}
				}
				return r
			}))
		}
		return m, tea.Batch(cmds...)

	case liveTasksMsg:
		m.applyLiveTasks(msg.tasks)
		return m, nil

	case liveDetailMsg:
		if m.view == viewDetail && m.detail.result != nil && m.detail.result.Task.ID == msg.result.Task.ID {
			y := m.detail.vp.YOffset
			m.detail.setResult(&msg.result)
			m.detail.vp.SetYOffset(y)
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m *Model) relayout() {
	lh := m.listHeight()
	dh := m.detailHeight()
	m.list.setSize(m.width, lh)
	m.detail.setSize(m.width, dh)
	m.graph.setSize(m.width, lh)
}

func (m *Model) listHeight() int {
	return m.height - 3 // title + status bar
}

func (m *Model) detailHeight() int {
	return m.height - 3
}

func (m Model) filteredTasks() []task.Task {
	if m.includeDone {
		return m.tasks
	}
	out := make([]task.Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		if !doneStatuses[string(t.Status)] {
			out = append(out, t)
		}
	}
	return out
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// handle input mode first
	if m.inputMode != inputNone {
		return m.handleInputKey(msg)
	}

	if m.showHelp {
		m.showHelp = false
		return m, nil
	}

	key := msg.String()

	switch m.view {
	case viewList:
		return m.handleListKey(key)
	case viewDetail:
		return m.handleDetailKey(key)
	case viewGraph:
		return m.handleGraphKey(key)
	}
	return m, nil
}

func (m Model) handleListKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		m.list.moveDown()
	case "k", "up":
		m.list.moveUp()
	case "g", "r":
		m.statusMsg = "Refreshing…"
		return m, m.loadTasks()
	case "a":
		m.includeDone = !m.includeDone
		filtered := m.filteredTasks()
		m.list.includeDone = m.includeDone
		m.list.rebuildWithTasks(filtered)
	case "tab", " ":
		m.list.toggleSection()
	case "enter":
		t := m.list.taskAtCursor()
		if t != nil {
			m.detail.loading = true
			return m, m.loadDetail(t.ID)
		}
	case "v":
		m.view = viewGraph
	case "x":
		t := m.list.taskAtCursor()
		if t != nil {
			return m, m.cmdDone(t.ID)
		}
	case "C":
		t := m.list.taskAtCursor()
		if t != nil {
			return m, m.cmdSetStatus(t.ID, "in_progress")
		}
	case "S":
		t := m.list.taskAtCursor()
		if t != nil {
			m.inputMode = inputStatus
			m.inputTaskID = t.ID
			m.statusIdx = 0
			m.inputPrompt = fmt.Sprintf("Status for %s", t.ID)
		}
	case "m":
		t := m.list.taskAtCursor()
		if t != nil {
			m.inputMode = inputComment
			m.inputTaskID = t.ID
			m.inputValue = ""
			m.inputPrompt = fmt.Sprintf("Comment on %s", t.ID)
		}
	case "c":
		m.inputMode = inputCreate
		m.inputStep = 0
		m.inputValue = ""
		m.inputPrompt = "New task title"
	case "y":
		t := m.list.taskAtCursor()
		if t != nil {
			m.statusMsg = fmt.Sprintf("Copied: %s", t.ID)
		}
	case "?":
		m.showHelp = !m.showHelp
	}
	return m, nil
}

func (m Model) handleDetailKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q", "esc":
		m.view = viewList
	case "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		m.detail.scrollDown()
	case "k", "up":
		m.detail.scrollUp()
	case "g", "r":
		if m.detail.result != nil {
			m.detail.loading = true
			return m, m.loadDetail(m.detail.result.Task.ID)
		}
	case "x":
		if m.detail.result != nil {
			return m, m.cmdDone(m.detail.result.Task.ID)
		}
	case "C":
		if m.detail.result != nil {
			return m, m.cmdSetStatus(m.detail.result.Task.ID, "in_progress")
		}
	case "S":
		if m.detail.result != nil {
			m.inputMode = inputStatus
			m.inputTaskID = m.detail.result.Task.ID
			m.statusIdx = 0
			m.inputPrompt = fmt.Sprintf("Status for %s", m.detail.result.Task.ID)
		}
	case "m":
		if m.detail.result != nil {
			m.inputMode = inputComment
			m.inputTaskID = m.detail.result.Task.ID
			m.inputValue = ""
			m.inputPrompt = fmt.Sprintf("Comment on %s", m.detail.result.Task.ID)
		}
	case "c":
		m.inputMode = inputCreate
		m.inputStep = 0
		m.inputValue = ""
		m.inputPrompt = "New task title"
	case "?":
		m.showHelp = !m.showHelp
	}
	return m, nil
}

func (m Model) handleGraphKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q", "esc":
		m.view = viewList
	case "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		m.graph.moveDown()
	case "k", "up":
		m.graph.moveUp()
	case "g", "r":
		return m, m.loadTasks()
	case "enter":
		id := m.graph.taskIDAtCursor()
		if id != "" {
			m.detail.loading = true
			return m, m.loadDetail(id)
		}
	case "s":
		m.view = viewList
	case "a":
		m.includeDone = !m.includeDone
		filtered := m.filteredTasks()
		m.graph.rebuild(filtered)
	case "?":
		m.showHelp = !m.showHelp
	}
	return m, nil
}

func (m Model) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.inputMode == inputStatus {
		switch key {
		case "j", "down":
			m.statusIdx = (m.statusIdx + 1) % len(m.statusChoices)
		case "k", "up":
			m.statusIdx = (m.statusIdx - 1 + len(m.statusChoices)) % len(m.statusChoices)
		case "enter":
			choice := m.statusChoices[m.statusIdx]
			id := m.inputTaskID
			m.inputMode = inputNone
			return m, m.cmdSetStatus(id, choice)
		case "esc", "ctrl+c":
			m.inputMode = inputNone
		}
		return m, nil
	}

	switch key {
	case "enter":
		switch m.inputMode {
		case inputComment:
			if strings.TrimSpace(m.inputValue) != "" {
				id := m.inputTaskID
				body := m.inputValue
				m.inputMode = inputNone
				m.inputValue = ""
				return m, m.cmdComment(id, body)
			}
			m.inputMode = inputNone
		case inputCreate:
			if strings.TrimSpace(m.inputValue) != "" {
				title := m.inputValue
				m.inputMode = inputNone
				m.inputValue = ""
				return m, m.cmdCreate(title)
			}
			m.inputMode = inputNone
		}
	case "esc", "ctrl+c":
		m.inputMode = inputNone
		m.inputValue = ""
	case "backspace", "ctrl+h":
		if len(m.inputValue) > 0 {
			runes := []rune(m.inputValue)
			m.inputValue = string(runes[:len(runes)-1])
		}
	default:
		if len(msg.Runes) > 0 {
			m.inputValue += string(msg.Runes)
		}
	}
	return m, nil
}

// commands

func (m Model) cmdDone(id string) tea.Cmd {
	return func() tea.Msg {
		t, err := m.store.Get(context.Background(), id)
		if err != nil {
			return errMsg{err}
		}
		done := task.StatusDone
		_, err = m.store.Update(context.Background(), t.ID, task.UpdateOpts{Status: &done})
		if err != nil {
			return errMsg{err}
		}
		return actionDoneMsg{fmt.Sprintf("Done: %s", id)}
	}
}

func (m Model) cmdSetStatus(id, status string) tea.Cmd {
	return func() tea.Msg {
		s := task.Status(status)
		_, err := m.store.Update(context.Background(), id, task.UpdateOpts{Status: &s})
		if err != nil {
			return errMsg{err}
		}
		return actionDoneMsg{fmt.Sprintf("%s → %s", id, status)}
	}
}

func (m Model) cmdComment(id, body string) tea.Cmd {
	return func() tea.Msg {
		_, err := m.store.AddComment(context.Background(), id, body, nil)
		if err != nil {
			return errMsg{err}
		}
		return actionDoneMsg{fmt.Sprintf("Comment added to %s", id)}
	}
}

func (m Model) cmdCreate(title string) tea.Cmd {
	return func() tea.Msg {
		_, err := m.store.Create(context.Background(), title, task.CreateOpts{})
		if err != nil {
			return errMsg{err}
		}
		return actionDoneMsg{"Created: " + title}
	}
}

// View

func (m Model) View() string {
	if m.width == 0 {
		return "Loading…"
	}

	var body string
	switch {
	case m.showHelp:
		body = m.helpView()
	case m.inputMode != inputNone:
		body = m.inputView()
	case m.view == viewDetail:
		body = m.detail.view()
	case m.view == viewGraph:
		body = m.graph.view()
	default:
		body = m.list.view()
	}

	title := m.titleBar()
	status := m.statusBar()

	return lipgloss.JoinVertical(lipgloss.Left, title, body, status)
}

func (m Model) titleBar() string {
	var title string
	switch m.view {
	case viewDetail:
		if m.detail.result != nil {
			title = "TT  " + styleMuted.Render(m.detail.result.Task.ID+" "+m.detail.result.Task.Title)
		} else {
			title = "TT"
		}
	case viewGraph:
		title = "TT  " + styleMuted.Render("graph")
	default:
		doneLabel := ""
		if m.includeDone {
			doneLabel = styleMuted.Render("  [+done]")
		}
		title = styleTitle.Render("TT") + doneLabel
	}
	return styleStatusBar.Width(m.width).Render(title)
}

func (m Model) statusBar() string {
	msg := m.statusMsg
	if m.errMsg != "" {
		msg = lipgloss.NewStyle().Foreground(colorP0).Render("Error: " + m.errMsg)
	}
	if msg == "" {
		msg = m.keyHints()
	}
	return styleStatusBar.Width(m.width).Render(msg)
}

func (m Model) keyHints() string {
	switch m.view {
	case viewList:
		return "j/k navigate  enter open  x done  C claim  S status  m comment  c create  v graph  a toggle done  ? help"
	case viewDetail:
		return "j/k scroll  q back  x done  C claim  S status  m comment  c create  g refresh  ? help"
	case viewGraph:
		return "j/k navigate  enter open  s list  a toggle done  g refresh  q back  ? help"
	}
	return ""
}

func (m Model) inputView() string {
	var sb strings.Builder
	h := m.height - 3

	if m.inputMode == inputStatus {
		sb.WriteString(styleInputPrompt.Render(m.inputPrompt) + "\n\n")
		for i, s := range m.statusChoices {
			icon := statusIcon(s)
			label := statusLabel(s)
			line := fmt.Sprintf("  %s  %s", icon, label)
			if i == m.statusIdx {
				line = styleCursor.Width(m.width - 2).Render(line)
			} else {
				line = "  " + styleMuted.Render(icon) + "  " + label
			}
			sb.WriteString(line + "\n")
		}
		// pad to height
		lines := strings.Count(sb.String(), "\n")
		for lines < h {
			sb.WriteByte('\n')
			lines++
		}
		return sb.String()
	}

	// text input
	cursor := "█"
	prompt := styleInputPrompt.Render(m.inputPrompt+": ") + m.inputValue + cursor
	sb.WriteString("\n\n  " + prompt + "\n")
	return sb.String()
}

func (m Model) helpView() string {
	lines := []string{
		styleTitle.Render("TT Keybindings") + "\n",
		"  j / ↓       move down",
		"  k / ↑       move up",
		"  enter       open task detail",
		"  q / esc     back / quit",
		"  g / r       refresh",
		"  a           toggle done/cancelled tasks",
		"  tab / space collapse/expand section",
		"  v           graph view",
		"  s           list view (from graph)",
		"",
		styleTitle.Render("Actions") + "\n",
		"  x           mark done",
		"  C           claim (set in_progress)",
		"  S           change status",
		"  m           add comment",
		"  c           create new task",
		"  y           copy task ID",
		"",
		"  ?           toggle this help",
		"  ctrl+c      quit",
	}
	return strings.Join(lines, "\n")
}

// reloadLive runs a load command and maps its result to the live variant,
// which updates data in place instead of resetting the view.
func (m Model) reloadLive(load tea.Cmd, wrap func(tea.Msg) tea.Msg) tea.Cmd {
	return func() tea.Msg { return wrap(load()) }
}
