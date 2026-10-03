package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/jrdn/tt/internal/task"
)

type showResult struct {
	Task      task.Task
	Subtasks  []task.Task
	Relations []task.Relation
	Comments  []task.Comment
}

type detailModel struct {
	vp      viewport.Model
	width   int
	height  int
	result  *showResult
	loading bool
}

func newDetailModel(width, height int) detailModel {
	vp := viewport.New(width, height)
	return detailModel{vp: vp, width: width, height: height}
}

func (m *detailModel) setSize(w, h int) {
	m.width = w
	m.height = h
	m.vp.Width = w
	m.vp.Height = h
}

func (m *detailModel) setResult(r *showResult) {
	m.result = r
	m.loading = false
	m.vp.SetContent(m.render())
	m.vp.GotoTop()
}

func (m *detailModel) scrollUp() {
	m.vp.ScrollUp(3)
}

func (m *detailModel) scrollDown() {
	m.vp.ScrollDown(3)
}

func (m *detailModel) view() string {
	if m.loading {
		return styleMuted.Render("  Loading…")
	}
	if m.result == nil {
		return styleMuted.Render("  No task selected.")
	}
	return m.vp.View()
}

func (m *detailModel) render() string {
	if m.result == nil {
		return ""
	}
	r := m.result
	t := r.Task

	var sb strings.Builder
	w := m.width

	// Title
	sb.WriteString(styleTitle.Width(w).Render(t.Title))
	sb.WriteByte('\n')
	sb.WriteString(styleMuted.Render(strings.Repeat("─", min(60, w))))
	sb.WriteByte('\n')
	sb.WriteByte('\n')

	// Metadata fields
	field := func(label, value string, style ...lipgloss.Style) {
		if value == "" {
			return
		}
		lbl := styleMuted.Render(fmt.Sprintf("%-14s", label+":"))
		val := value
		if len(style) > 0 {
			val = style[0].Render(value)
		}
		sb.WriteString(lbl + val + "\n")
	}

	field("ID", t.ID, styleID)
	field("Status", string(t.Status))
	field("Priority", fmt.Sprintf("%s (%s)", priorityLabel(t.Priority), priorityName(t.Priority)),
		priorityStyle(t.Priority))
	if t.Assignee != nil {
		field("Assignee", *t.Assignee)
	}
	if t.ParentID != nil {
		field("Parent", *t.ParentID, styleID)
	}
	if t.DueDate != nil {
		field("Due", string(*t.DueDate))
	}
	created := fmtRelativeTime(t.CreatedAt)
	if t.CreatedBy != nil {
		field("Created", fmt.Sprintf("%s  %s", *t.CreatedBy, created))
	} else if created != "" {
		field("Created", created)
	}
	sb.WriteByte('\n')

	// Description
	if t.Description != nil && strings.TrimSpace(*t.Description) != "" {
		sb.WriteString(styleHeader.Render("Description"))
		sb.WriteByte('\n')
		sb.WriteString(strings.TrimRight(*t.Description, "\n"))
		sb.WriteByte('\n')
		sb.WriteByte('\n')
	}

	// Subtasks
	if len(r.Subtasks) > 0 {
		sb.WriteString(styleHeader.Render("Subtasks"))
		sb.WriteByte('\n')
		for _, sub := range r.Subtasks {
			icon := styleMuted.Render(statusIcon(string(sub.Status)))
			id := styleID.Render(fmt.Sprintf("%-8s", sub.ID))
			sb.WriteString("  " + icon + "  " + id + sub.Title + "\n")
		}
		sb.WriteByte('\n')
	}

	// Relations
	if len(r.Relations) > 0 {
		sb.WriteString(styleHeader.Render("Relations"))
		sb.WriteByte('\n')
		for _, rel := range r.Relations {
			relType := styleMuted.Render(fmt.Sprintf("%-12s", rel.Type))
			from := styleID.Render(rel.FromID)
			to := styleID.Render(rel.ToID)
			sb.WriteString("  " + relType + from + " → " + to + "\n")
		}
		sb.WriteByte('\n')
	}

	// Comments
	if len(r.Comments) > 0 {
		sb.WriteString(styleHeader.Render("Comments"))
		sb.WriteByte('\n')
		for _, c := range r.Comments {
			author := ""
			if c.Author != nil {
				author = *c.Author
			}
			date := fmtRelativeTime(c.CreatedAt)
			sb.WriteString("  " + lipgloss.NewStyle().Bold(true).Render(author))
			sb.WriteString("  " + styleMuted.Render(date) + "\n")
			body := strings.TrimRight(c.Body, "\n")
			for _, line := range strings.Split(body, "\n") {
				sb.WriteString("    " + line + "\n")
			}
			sb.WriteByte('\n')
		}
	}

	return sb.String()
}

func priorityName(p int) string {
	switch p {
	case 0:
		return "Critical"
	case 1:
		return "High"
	case 2:
		return "Medium"
	case 3:
		return "Low"
	default:
		return "Unknown"
	}
}
