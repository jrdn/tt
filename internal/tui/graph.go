package tui

import (
	"fmt"
	"strings"

	"github.com/jrdn/tt/internal/task"
)

type graphModel struct {
	lines  []graphLine
	cursor int
	offset int
	height int
	width  int
}

type graphLine struct {
	text   string
	taskID string
}

func newGraphModel(tasks []task.Task, width, height int) graphModel {
	m := graphModel{width: width, height: height}
	m.rebuild(tasks)
	return m
}

func (m *graphModel) rebuild(tasks []task.Task) {
	index := map[string]*task.Task{}
	for i := range tasks {
		t := tasks[i]
		index[t.ID] = &t
	}

	children := map[string][]task.Task{}
	roots := []task.Task{}
	hasParent := map[string]bool{}

	for _, t := range tasks {
		if t.ParentID != nil && *t.ParentID != "" {
			if _, ok := index[*t.ParentID]; ok {
				children[*t.ParentID] = append(children[*t.ParentID], t)
				hasParent[t.ID] = true
			}
		}
	}
	for _, t := range tasks {
		if !hasParent[t.ID] {
			roots = append(roots, t)
		}
	}

	m.lines = nil
	seen := map[string]bool{}
	for i, root := range roots {
		m.insertNode(root, "", i == len(roots)-1, children, seen)
	}

	// remaining tasks not reachable from roots (broken parent refs)
	for _, t := range tasks {
		if !seen[t.ID] {
			m.insertNode(t, "", true, children, seen)
		}
	}

	if m.cursor >= len(m.lines) {
		m.cursor = max(0, len(m.lines)-1)
	}
}

func (m *graphModel) insertNode(t task.Task, prefix string, isLast bool, children map[string][]task.Task, seen map[string]bool) {
	if seen[t.ID] {
		return
	}
	seen[t.ID] = true

	connector := ""
	childPrefix := ""
	if prefix != "" || !isLast {
		// non-root node
		if isLast {
			connector = prefix + "└─ "
			childPrefix = prefix + "   "
		} else {
			connector = prefix + "├─ "
			childPrefix = prefix + "│  "
		}
	} else if prefix == "" && !isLast {
		connector = "├─ "
		childPrefix = "│  "
	}

	icon := styleMuted.Render(statusIcon(string(t.Status)))
	id := styleID.Render(fmt.Sprintf("%-8s", t.ID))
	pLabel := priorityStyle(t.Priority).Render(priorityLabel(t.Priority))
	line := connector + icon + " " + id + " " + pLabel + "  " + t.Title

	m.lines = append(m.lines, graphLine{text: line, taskID: t.ID})

	kids := children[t.ID]
	for i, child := range kids {
		m.insertNode(child, childPrefix, i == len(kids)-1, children, seen)
	}
}

func (m *graphModel) setSize(w, h int) {
	m.width = w
	m.height = h
	m.clampOffset()
}

func (m *graphModel) moveUp() {
	if m.cursor > 0 {
		m.cursor--
		m.clampOffset()
	}
}

func (m *graphModel) moveDown() {
	if m.cursor < len(m.lines)-1 {
		m.cursor++
		m.clampOffset()
	}
}

func (m *graphModel) taskIDAtCursor() string {
	if m.cursor >= len(m.lines) {
		return ""
	}
	return m.lines[m.cursor].taskID
}

func (m *graphModel) clampOffset() {
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

func (m *graphModel) view() string {
	if len(m.lines) == 0 {
		return styleMuted.Render("  No tasks.")
	}
	var sb strings.Builder
	end := min(m.offset+m.height, len(m.lines))
	for i := m.offset; i < end; i++ {
		line := m.lines[i].text
		if i == m.cursor {
			// plain version for cursor highlight
			plain := stripSimple(m.lines[i].text)
			line = styleCursor.Width(m.width).Render(plain)
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// stripSimple removes basic styling for cursor highlight rendering.
// Since we build lines with lipgloss-rendered segments, we need the raw text
// for the cursor row. We just use the task data directly.
func stripSimple(s string) string {
	// Remove ANSI escape sequences for cursor highlight
	result := strings.Builder{}
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
}
