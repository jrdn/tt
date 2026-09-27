package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

func newEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit a task in $EDITOR",
		Long: `Edit a task in $EDITOR.

The ID can be a plain task ID or a qualified ID (<db_name>.<task_id>).`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			qt, err := resolveTask(args[0])
			if err != nil {
				return err
			}
			defer qt.Close()

			t, err := qt.store.Get(cmd.Context(), qt.id)
			if err != nil {
				return err
			}

			f, err := os.CreateTemp("", "tt-*.md")
			if err != nil {
				return err
			}
			tmpPath := f.Name()
			defer os.Remove(tmpPath)
			if _, err := f.WriteString(serializeTask(t)); err != nil {
				f.Close()
				return err
			}
			f.Close()

			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}
			c := exec.Command(editor, tmpPath)
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			if err := c.Run(); err != nil {
				return err
			}

			data, err := os.ReadFile(tmpPath)
			if err != nil {
				return err
			}

			updated, err := parseTaskFile(t, string(data))
			if err != nil {
				return err
			}

			updated.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			if err := qt.store.Save(cmd.Context(), updated); err != nil {
				return err
			}

			fmt.Printf("updated %s\n", updated.ID)
			return nil
		},
	}
}

func serializeTask(t *task.Task) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("status: %s\n", t.Status))
	b.WriteString(fmt.Sprintf("priority: %d\n", t.Priority))
	if t.Assignee != nil {
		b.WriteString(fmt.Sprintf("assignee: %s\n", *t.Assignee))
	} else {
		b.WriteString("assignee:\n")
	}
	if t.DueDate != nil {
		b.WriteString(fmt.Sprintf("due: %s\n", *t.DueDate))
	} else {
		b.WriteString("due:\n")
	}
	if t.ParentID != nil {
		b.WriteString(fmt.Sprintf("parent: %s\n", *t.ParentID))
	} else {
		b.WriteString("parent:\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(fmt.Sprintf("# %s\n", t.Title))
	if t.Description != nil && *t.Description != "" {
		b.WriteString("\n")
		b.WriteString(*t.Description)
		if !strings.HasSuffix(*t.Description, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func parseTaskFile(original *task.Task, content string) (*task.Task, error) {
	content = strings.TrimPrefix(content, "---\n")
	idx := strings.Index(content, "\n---\n")
	if idx < 0 {
		return nil, fmt.Errorf("missing frontmatter closing ---")
	}
	fm := content[:idx]
	body := strings.TrimPrefix(content[idx+5:], "\n")

	t := *original

	for _, line := range strings.Split(fm, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		switch k {
		case "status":
			newStatus := task.Status(v)
			if newStatus != t.Status {
				t.Status = newStatus
				if newStatus == task.StatusDone || newStatus == task.StatusCancelled {
					if t.ClosedAt == nil {
						ts := time.Now().UTC().Format(time.RFC3339)
						t.ClosedAt = &ts
					}
				} else {
					t.ClosedAt = nil
				}
			}
		case "priority":
			if p, err := strconv.Atoi(v); err == nil {
				t.Priority = p
			}
		case "assignee":
			if v == "" {
				t.Assignee = nil
			} else {
				s := v
				t.Assignee = &s
			}
		case "due":
			if v == "" {
				t.DueDate = nil
			} else {
				s := v
				t.DueDate = task.DatePtr(&s)
			}
		case "parent":
			if v == "" {
				t.ParentID = nil
			} else {
				s := v
				t.ParentID = &s
			}
		}
	}

	// find title from first # heading
	lines := strings.Split(body, "\n")
	titleIdx := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "# ") {
			t.Title = strings.TrimPrefix(line, "# ")
			titleIdx = i
			break
		}
	}

	if titleIdx >= 0 {
		desc := strings.TrimSpace(strings.Join(lines[titleIdx+1:], "\n"))
		if desc == "" {
			t.Description = nil
		} else {
			t.Description = &desc
		}
	}

	return &t, nil
}
