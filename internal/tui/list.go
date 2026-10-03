package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/jrdn/tt/internal/task"
)

// row is one navigable item in the list — either a section header or a task.
type row struct {
	isSectionHeader bool
	sectionStatus   string
	sectionCount    int
	collapsed       bool // only meaningful on header rows
	task            *task.Task
}

// listModel holds the scrollable task list state.
type listModel struct {
	rows        []row
	allTasks    []task.Task // full unfiltered task list for rebuilding
	cursor      int
	offset      int // first visible row index
	height      int // visible rows available
	width       int
	includeDone bool
}

var statusOrder = []string{"ready", "in_progress", "in_review", "open", "backlog"}
var doneStatuses = map[string]bool{"done": true, "cancelled": true}

func newListModel(tasks []task.Task, width, height int, includeDone bool) listModel {
	m := listModel{width: width, height: height, includeDone: includeDone}
	m.rebuild(tasks)
	return m
}

func (m *listModel) rebuild(tasks []task.Task) {
	m.allTasks = tasks
	// track which headers were collapsed before rebuilding
	collapsed := map[string]bool{}
	for _, r := range m.rows {
		if r.isSectionHeader && r.collapsed {
			collapsed[r.sectionStatus] = true
		}
	}

	byStatus := map[task.Status][]task.Task{}
	for _, t := range tasks {
		byStatus[t.Status] = append(byStatus[t.Status], task.Task(t))
	}

	// collect statuses in display order
	seen := map[string]bool{}
	order := append([]string{}, statusOrder...)
	for _, t := range tasks {
		s := string(t.Status)
		if !seen[s] {
			seen[s] = true
			found := false
			for _, o := range order {
				if o == s {
					found = true
					break
				}
			}
			if !found && !doneStatuses[s] {
				order = append(order, s)
			}
		}
	}
	if m.includeDone {
		order = append(order, "done", "cancelled")
	}

	m.rows = nil
	for _, status := range order {
		group := byStatus[task.Status(status)]
		if len(group) == 0 {
			continue
		}
		isCollapsed := collapsed[status]
		m.rows = append(m.rows, row{
			isSectionHeader: true,
			sectionStatus:   status,
			sectionCount:    len(group),
			collapsed:       isCollapsed,
		})
		if !isCollapsed {
			for i := range group {
				t := group[i]
				m.rows = append(m.rows, row{task: &t})
			}
		}
	}

	if m.cursor >= len(m.rows) {
		m.cursor = max(0, len(m.rows)-1)
	}
	m.clampOffset()
}

func (m *listModel) setSize(w, h int) {
	m.width = w
	m.height = h
	m.clampOffset()
}

func (m *listModel) moveUp() {
	if m.cursor > 0 {
		m.cursor--
		m.clampOffset()
	}
}

func (m *listModel) moveDown() {
	if m.cursor < len(m.rows)-1 {
		m.cursor++
		m.clampOffset()
	}
}

func (m *listModel) toggleSection() {
	if m.cursor >= len(m.rows) {
		return
	}
	r := &m.rows[m.cursor]
	if !r.isSectionHeader {
		// find the header above
		for i := m.cursor - 1; i >= 0; i-- {
			if m.rows[i].isSectionHeader {
				r = &m.rows[i]
				m.cursor = i
				break
			}
		}
	}
	if r.isSectionHeader {
		r.collapsed = !r.collapsed
		m.rebuild(m.allTasks)
	}
}

// taskAtCursor returns the task at the cursor, or nil if on a header.
func (m *listModel) taskAtCursor() *task.Task {
	if m.cursor >= len(m.rows) {
		return nil
	}
	r := m.rows[m.cursor]
	if r.isSectionHeader {
		return nil
	}
	return r.task
}

func (m *listModel) clampOffset() {
	// ensure cursor is visible
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+m.height {
		m.offset = m.cursor - m.height + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *listModel) view() string {
	if len(m.rows) == 0 {
		return styleMuted.Render("  No tasks found.")
	}

	var sb strings.Builder
	end := min(m.offset+m.height, len(m.rows))
	for i := m.offset; i < end; i++ {
		r := m.rows[i]
		selected := i == m.cursor
		line := m.renderRow(r, selected)
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func (m *listModel) renderRow(r row, selected bool) string {
	if r.isSectionHeader {
		return m.renderHeader(r, selected)
	}
	return m.renderTask(r, selected)
}

func (m *listModel) renderHeader(r row, selected bool) string {
	label := statusLabel(r.sectionStatus)
	arrow := "▼"
	if r.collapsed {
		arrow = "▶"
	}
	text := fmt.Sprintf(" %s %s (%d) ", arrow, label, r.sectionCount)
	styled := sectionStyle(r.sectionStatus).Width(m.width).Render(text)
	if selected {
		// add cursor highlight
		styled = styleCursor.Width(m.width).Render(text)
	}
	return styled
}

func (m *listModel) renderTask(r row, selected bool) string {
	t := r.task
	idStr := styleID.Render(truncate(t.ID, 7))
	pLabel := priorityStyle(t.Priority).Render(fmt.Sprintf("%-2s", priorityLabel(t.Priority)))

	// compute available width for title
	// " " + id(7) + " " + priority(2) + "  " + title + "  " + assignee
	metaWidth := 1 + 7 + 1 + 2 + 2
	assigneeStr := ""
	if t.Assignee != nil {
		assigneeStr = *t.Assignee
		if utf8.RuneCountInString(assigneeStr) > 18 {
			assigneeStr = truncate(assigneeStr, 18)
		}
	}
	assigneeWidth := 0
	if assigneeStr != "" {
		assigneeWidth = utf8.RuneCountInString(assigneeStr) + 2
	}

	titleWidth := m.width - metaWidth - assigneeWidth
	if titleWidth < 8 {
		titleWidth = 8
	}
	titleStr := truncate(t.Title, titleWidth)
	titleStr = padRight(titleStr, titleWidth)

	line := " " + idStr + " " + pLabel + "  " + titleStr
	if assigneeStr != "" {
		line += "  " + styleMuted.Render(assigneeStr)
	}

	if selected {
		// strip existing ANSI and re-render with cursor style
		plain := " " + truncate(t.ID, 7) + " " + priorityLabel(t.Priority) + "  " + truncate(t.Title, titleWidth)
		plain = padRight(plain, m.width)
		if assigneeStr != "" {
			plain = padRight(plain[:len(plain)-assigneeWidth], m.width-assigneeWidth)
		}
		return styleCursor.Width(m.width).Render(plain)
	}

	return line
}

func statusLabel(s string) string {
	switch s {
	case "in_progress":
		return "In Progress"
	case "in_review":
		return "In Review"
	default:
		words := strings.ReplaceAll(s, "_", " ")
		if len(words) == 0 {
			return words
		}
		return strings.ToUpper(words[:1]) + words[1:]
	}
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 1 {
		return string(runes[:n])
	}
	return string(runes[:n-1]) + "…"
}

func padRight(s string, n int) string {
	w := utf8.RuneCountInString(s)
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}


// rebuildWithTasks replaces the task list and rebuilds rows, preserving collapse state.
func (m *listModel) rebuildWithTasks(tasks []task.Task) {
	m.rebuild(tasks)
}

// fmtRelativeTime returns a short human-readable relative time string.
func fmtRelativeTime(iso string) string {
	if iso == "" {
		return ""
	}
	// just return the date part for simplicity
	if len(iso) >= 10 {
		return iso[:10]
	}
	return iso
}

var _ = lipgloss.NewStyle() // ensure lipgloss is used
