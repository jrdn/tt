package cmd

import (
	"fmt"

	"github.com/fatih/color"
	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

var relTypeColors = map[string]*color.Color{
	"blocks":     color.New(color.FgRed, color.Bold),
	"related":    color.New(color.FgCyan),
	"duplicates": color.New(color.FgYellow),
}

func newGraphCmd() *cobra.Command {
	var (
		status   string
		assignee string
		all      bool
	)

	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Show tasks as a relationship graph",
		RunE: func(cmd *cobra.Command, args []string) error {
			tasks, err := task.List(db, task.ListOpts{
				Status:   status,
				Assignee: assignee,
				All:      all,
			})
			if err != nil {
				return err
			}
			if len(tasks) == 0 {
				fmt.Println("no tasks")
				return nil
			}
			return renderGraph(tasks)
		},
	}

	cmd.Flags().StringVar(&status, "status", "", "Filter by status")
	cmd.Flags().StringVarP(&assignee, "assignee", "a", "", "Filter by assignee")
	cmd.Flags().BoolVar(&all, "all", false, "Include all statuses")

	return cmd
}

type graphCtx struct {
	byID       map[string]*task.Task
	childrenOf map[string][]*task.Task
	relsOf     map[string][]task.Relation
}

func renderGraph(tasks []task.Task) error {
	byID := map[string]*task.Task{}
	for i := range tasks {
		byID[tasks[i].ID] = &tasks[i]
	}

	childrenOf := map[string][]*task.Task{}
	var roots []*task.Task
	for i := range tasks {
		t := &tasks[i]
		if t.ParentID == nil || byID[*t.ParentID] == nil {
			roots = append(roots, t)
		} else {
			childrenOf[*t.ParentID] = append(childrenOf[*t.ParentID], t)
		}
	}

	// Collect outgoing relations; fetch any targets outside the visible set.
	relsOf := map[string][]task.Relation{}
	missing := map[string]struct{}{}
	for _, t := range tasks {
		all, err := task.GetRelations(db, t.ID)
		if err != nil {
			return err
		}
		for _, r := range all {
			if r.FromID == t.ID {
				relsOf[t.ID] = append(relsOf[t.ID], r)
				if _, ok := byID[r.ToID]; !ok {
					missing[r.ToID] = struct{}{}
				}
			}
		}
	}
	for id := range missing {
		if t, err := task.Get(db, id); err == nil {
			byID[id] = t
		}
	}

	g := &graphCtx{byID: byID, childrenOf: childrenOf, relsOf: relsOf}

	for i, root := range roots {
		if i > 0 {
			fmt.Println()
		}
		g.node(root, "", "")
	}
	return nil
}

// node renders a task and recurses into its children and relations.
// nodePrefix is printed before the ● glyph; childIndent is the base indent
// passed to children and relation edges.
func (g *graphCtx) node(t *task.Task, nodePrefix, childIndent string) {
	p := t.Priority
	if p < 0 || p > 3 {
		p = 2
	}
	priLabel := priorityColors[p].Sprint(priorityLabels[p])

	sc, ok := statusColors[t.Status]
	if !ok {
		sc = color.New(color.Reset)
	}
	statusStr := sc.Sprintf("%-13s", string(t.Status))

	assigneeStr := ""
	if t.Assignee != nil {
		assigneeStr = color.New(color.FgHiBlack).Sprint("  @" + *t.Assignee)
	}

	dot := color.New(color.FgWhite, color.Bold).Sprint("●")
	fmt.Printf("%s%s %s  %s  %s  %s%s\n",
		nodePrefix, dot, t.ID, priLabel, statusStr, t.Title, assigneeStr)

	children := g.childrenOf[t.ID]
	rels := g.relsOf[t.ID]

	for i, child := range children {
		isLast := i == len(children)-1 && len(rels) == 0
		if isLast {
			g.node(child, childIndent+"└── ", childIndent+"    ")
		} else {
			g.node(child, childIndent+"├── ", childIndent+"│   ")
		}
	}

	for i, rel := range rels {
		isLast := i == len(rels)-1
		connector := "├── "
		if isLast {
			connector = "└── "
		}
		g.relEdge(rel, childIndent+connector)
	}
}

func (g *graphCtx) relEdge(rel task.Relation, prefix string) {
	relColor, ok := relTypeColors[rel.Type]
	if !ok {
		relColor = color.New(color.Reset)
	}

	typeLabel := relColor.Sprintf("[%s]", rel.Type)
	arrow := color.New(color.FgHiBlack).Sprint("──►")

	target := g.byID[rel.ToID]
	var targetStr string
	if target != nil {
		sc, ok := statusColors[target.Status]
		if !ok {
			sc = color.New(color.Reset)
		}
		targetStr = fmt.Sprintf("%s  %s  %s", target.ID, sc.Sprint(string(target.Status)), target.Title)
	} else {
		targetStr = rel.ToID
	}

	fmt.Printf("%s%s %s  %s\n", prefix, typeLabel, arrow, targetStr)
}
