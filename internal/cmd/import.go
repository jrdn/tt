package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

func newImportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import tasks from external sources",
	}
	cmd.AddCommand(newImportLinearCmd())
	return cmd
}

func newImportLinearCmd() *cobra.Command {
	var (
		token      string
		identifier string // e.g. ENG-123
		team       string // team key, e.g. ENG
		project    string // project name (substring match)
		assignee   string // assignee email
		state      string // Linear state name, e.g. "In Progress"
		dryRun     bool
	)

	cmd := &cobra.Command{
		Use:   "linear",
		Short: "Import issues from Linear",
		Long: `Import Linear issues into tt. At least one filter is required.

Examples:
  tt import linear --token $LINEAR_API_KEY --id ENG-123
  tt import linear --token $LINEAR_API_KEY --team ENG --state "In Progress"
  tt import linear --token $LINEAR_API_KEY --team ENG --assignee me@example.com
  tt import linear --token $LINEAR_API_KEY --team ENG --project "Backend"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if token == "" {
				return fmt.Errorf("--token is required (or set LINEAR_API_KEY env var)")
			}
			if identifier == "" && team == "" {
				return fmt.Errorf("at least one of --id or --team is required")
			}

			issues, err := fetchLinearIssues(token, linearFilters{
				identifier: identifier,
				team:       team,
				project:    project,
				assignee:   assignee,
				state:      state,
			})
			if err != nil {
				return fmt.Errorf("fetch from Linear: %w", err)
			}

			if len(issues) == 0 {
				fmt.Println("no matching issues found")
				return nil
			}

			for _, issue := range issues {
				existing, _ := store.FindByExternalRef(cmd.Context(), "linear", issue.ID)
				if existing != nil {
					if dryRun {
						fmt.Printf("skip  %s — already imported as %s\n", issue.Identifier, existing.ID)
					} else {
						fmt.Printf("skip  %s — already imported as %s\n", issue.Identifier, existing.ID)
					}
					continue
				}

				if dryRun {
					fmt.Printf("would import %s: %s\n", issue.Identifier, issue.Title)
					continue
				}

				opts := task.CreateOpts{
					Priority:    linearPriority(issue.Priority),
					PrioritySet: true,
				}
				if issue.Description != "" {
					opts.Description = &issue.Description
				}
				if issue.Assignee.Email != "" {
					opts.Assignee = &issue.Assignee.Email
				}
				if issue.DueDate != "" {
					opts.DueDate = &issue.DueDate
				}

				t, err := store.Create(cmd.Context(), issue.Title, opts)
				if err != nil {
					return fmt.Errorf("create task for %s: %w", issue.Identifier, err)
				}

				// Update status to match Linear's state
				s := linearStatus(issue.State.Type)
				if s != task.StatusOpen {
					if _, err := store.Update(cmd.Context(), t.ID, task.UpdateOpts{Status: &s}); err != nil {
						return fmt.Errorf("set status for %s: %w", issue.Identifier, err)
					}
				}

				url := "https://linear.app/issue/" + issue.Identifier
				if err := store.UpsertExternalRef(cmd.Context(), t.ID, "linear", issue.ID, &url); err != nil {
					return fmt.Errorf("save external ref for %s: %w", issue.Identifier, err)
				}

				fmt.Printf("imported %s → %s: %s\n", issue.Identifier, t.ID, issue.Title)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&token, "token", "", "Linear API key (or set LINEAR_API_KEY)")
	cmd.Flags().StringVar(&identifier, "id", "", "Import a single issue by identifier (e.g. ENG-123)")
	cmd.Flags().StringVar(&team, "team", "", "Team key to import from (e.g. ENG)")
	cmd.Flags().StringVar(&project, "project", "", "Filter by project name (substring match)")
	cmd.Flags().StringVar(&assignee, "assignee", "", "Filter by assignee email")
	cmd.Flags().StringVar(&state, "state", "", "Filter by state name (e.g. \"In Progress\")")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview what would be imported without creating tasks")

	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if !cmd.Flags().Changed("token") {
			if v := os.Getenv("LINEAR_API_KEY"); v != "" {
				token = v
			}
		}
		return nil
	}

	return cmd
}

type linearFilters struct {
	identifier string
	team       string
	project    string
	assignee   string
	state      string
}

type linearIssue struct {
	ID          string `json:"id"`
	Identifier  string `json:"identifier"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    int    `json:"priority"`
	DueDate     string `json:"dueDate"`
	State       struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"state"`
	Assignee struct {
		Email string `json:"email"`
	} `json:"assignee"`
}

func fetchLinearIssues(token string, f linearFilters) ([]linearIssue, error) {
	// Build the filter object for the GraphQL query
	var filterParts []string

	if f.identifier != "" {
		// identifier looks like ENG-123; Linear stores team key + number separately
		filterParts = append(filterParts, fmt.Sprintf(`identifier: { eq: %q }`, f.identifier))
	}
	if f.team != "" {
		filterParts = append(filterParts, fmt.Sprintf(`team: { key: { eq: %q } }`, f.team))
	}
	if f.project != "" {
		filterParts = append(filterParts, fmt.Sprintf(`project: { name: { containsIgnoreCase: %q } }`, f.project))
	}
	if f.assignee != "" {
		filterParts = append(filterParts, fmt.Sprintf(`assignee: { email: { eq: %q } }`, f.assignee))
	}
	if f.state != "" {
		filterParts = append(filterParts, fmt.Sprintf(`state: { name: { eq: %q } }`, f.state))
	}

	filter := ""
	if len(filterParts) > 0 {
		filter = "filter: { " + strings.Join(filterParts, ", ") + " }"
	}

	query := fmt.Sprintf(`{
		issues(%s, first: 250) {
			nodes {
				id
				identifier
				title
				description
				priority
				dueDate
				state { name type }
				assignee { email }
			}
		}
	}`, filter)

	body, _ := json.Marshal(map[string]string{"query": query})
	req, err := http.NewRequest("POST", "https://api.linear.app/graphql", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Data struct {
			Issues struct {
				Nodes []linearIssue `json:"nodes"`
			} `json:"issues"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if len(result.Errors) > 0 {
		msgs := make([]string, len(result.Errors))
		for i, e := range result.Errors {
			msgs[i] = e.Message
		}
		return nil, fmt.Errorf("linear API error: %s", strings.Join(msgs, "; "))
	}

	return result.Data.Issues.Nodes, nil
}

// linearPriority maps Linear's 0-4 scale to tt's 0-3.
// Linear: 0=no priority, 1=urgent, 2=high, 3=medium, 4=low
// tt:     0=critical,    1=high,   2=normal,         3=low
func linearPriority(p int) int {
	switch p {
	case 1:
		return 0 // urgent → critical
	case 2:
		return 1 // high → high
	case 3:
		return 2 // medium → normal
	case 4:
		return 3 // low → low
	default:
		return 2 // no priority → normal
	}
}

// linearStatus maps Linear state types to tt statuses.
// Linear state types: triage, backlog, unstarted, started, completed, cancelled
func linearStatus(stateType string) task.Status {
	switch stateType {
	case "backlog", "triage":
		return task.StatusBacklog
	case "completed":
		return task.StatusDone
	case "cancelled":
		return task.StatusCancelled
	case "started":
		return task.StatusInProgress
	default: // unstarted
		return task.StatusOpen
	}
}

