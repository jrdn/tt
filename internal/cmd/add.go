package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

func newAddCmd() *cobra.Command {
	var (
		parent      string
		description string
		priority    int
		prioritySet bool
		createdBy   string
		assignee    string
		dueDate     string
	)

	cmd := &cobra.Command{
		Use:     "add [title] [description]",
		Aliases: []string{"new", "n"},
		Short:   "Create a new task",
		Args:    cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			// No title: open editor with blank template
			if len(args) == 0 {
				return addFromEditor()
			}

			title := args[0]
			if len(args) == 2 && !cmd.Flags().Changed("description") {
				description = args[1]
			}
			opts := task.CreateOpts{
				Priority:    priority,
				PrioritySet: prioritySet,
			}
			if parent != "" {
				opts.ParentID = &parent
			}
			if description != "" {
				opts.Description = &description
			}
			if createdBy != "" {
				opts.CreatedBy = &createdBy
			}
			if assignee != "" {
				opts.Assignee = &assignee
			}
			if dueDate != "" {
				opts.DueDate = &dueDate
			}

			t, err := task.Create(db, title, opts)
			if err != nil {
				return err
			}
			if jsonOutput {
				return printJSON(t)
			}
			fmt.Printf("created %s\n", t.ID)
			return nil
		},
	}

	cmd.Flags().StringVar(&parent, "parent", "", "Parent task ID")
	cmd.Flags().StringVarP(&description, "description", "d", "", "Task description")
	cmd.Flags().IntVarP(&priority, "priority", "p", 2, "Priority 0-3 (0=critical, 3=low)")
	cmd.Flags().StringVar(&createdBy, "created-by", "", "Creator handle")
	cmd.Flags().StringVar(&assignee, "assignee", "", "Assignee handle (e.g. jrdn, claude/opus4.7)")
	cmd.Flags().StringVar(&dueDate, "due", "", "Due date (YYYY-MM-DD)")

	cmd.PreRun = func(cmd *cobra.Command, args []string) {
		prioritySet = cmd.Flags().Changed("priority")
	}

	return cmd
}

func addFromEditor() error {
	stub := &task.Task{Status: task.StatusOpen, Priority: 2}
	template := serializeTask(stub)

	f, err := os.CreateTemp("", "tt-*.md")
	if err != nil {
		return err
	}
	tmpPath := f.Name()
	defer os.Remove(tmpPath)
	if _, err := f.WriteString(template); err != nil {
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

	parsed, err := parseTaskFile(stub, string(data))
	if err != nil {
		return err
	}
	if parsed.Title == "" {
		return fmt.Errorf("title is required")
	}

	opts := task.CreateOpts{
		Priority:    parsed.Priority,
		PrioritySet: true,
		Description: parsed.Description,
		ParentID:    parsed.ParentID,
		Assignee:    parsed.Assignee,
		DueDate:     parsed.DueDate,
	}
	t, err := task.Create(db, parsed.Title, opts)
	if err != nil {
		return err
	}
	if jsonOutput {
		return printJSON(t)
	}
	fmt.Printf("created %s\n", t.ID)
	return nil
}
