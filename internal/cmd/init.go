package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const sectionHeader = "## tt Task Manager"
const sectionEnd = "<!-- end tt -->"

const injectedContent = `## tt Task Manager

This project uses **tt** for task tracking. Tasks live in a local SQLite database at ` + "`~/.config/tt/<repo>.db`" + `.

### Key Commands

` + "```" + `bash
tt ls                                        # list open tasks
tt ls --status=in_progress                  # filter by status
tt add "title"                              # create a task
tt add "title" --assignee claude/opus4.7   # assign on create
tt show <id>                                # full detail: subtasks, relations, comments
tt done <id>                                # mark done
tt update <id> --status=in_progress         # update any field
tt update <id> --assignee <handle>          # reassign
tt comment <id> "note"                      # append progress note without editing description
tt relate <id> blocks <id>                  # link tasks (blocks | duplicates | related)
` + "```" + `

### Notes for Agents

- All commands accept ` + "`--json`" + ` for machine-readable output. ` + "`tt show <id> --json`" + ` returns task, subtasks, relations, and comments in one object.
- IDs are short 4-char base32 hashes. Prefix matching is supported — ` + "`tt show ab`" + ` works if unambiguous.
- ` + "`--assignee`" + ` accepts free-form handles: ` + "`jrdn`" + `, ` + "`claude/opus4.7`" + `, ` + "`lmstudio/qwen3-coder`" + `.
- Use ` + "`tt comment`" + ` to log reasoning and progress without overwriting the description. Always pass ` + "`--author`" + ` to identify yourself (e.g. ` + "`--author claude/opus4.7`" + `, ` + "`--author cursor/claude-sonnet`" + `) — default falls back to OS username, which is not meaningful for agents.
- Priority: 0=critical, 1=high, 2=normal (default), 3=low, 4=backlog.

### Resumability

Leave a trail so another agent can pick up your work if you crash or are interrupted. When working on a task, comment regularly with:

- What you've done so far
- What's still left
- The current branch name
- Commit hashes for any commits you've made
- Any gotchas, blockers, or context a fresh agent would need

` + "```" + `bash
tt c <id> "implemented auth middleware on branch feat/auth (a3f2c1b); still need to wire up the logout route and write tests" --author claude/opus4.7
` + "```" + `

Before ending a session on an in-progress task, always leave a comment summarizing the current state. ` + "`tt show <id>`" + ` is the first thing a resuming agent should run.

### Sync

` + "`tt sync pull`" + ` / ` + "`tt sync push`" + ` are available but should only be run on explicit request — sync is not automatic.

<!-- end tt -->`

func newInitCmd() *cobra.Command {
	var filePath string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Inject tt reference into AGENTS.md and/or CLAUDE.md",
		RunE: func(cmd *cobra.Command, args []string) error {
			var targets []string
			if filePath != "" {
				targets = []string{filePath}
			} else {
				targets = resolveTargetFiles()
			}

			for _, target := range targets {
				var existing string
				if data, err := os.ReadFile(target); err == nil {
					existing = string(data)
				} else if !os.IsNotExist(err) {
					return fmt.Errorf("read %s: %w", target, err)
				}

				result, inserted := injectSection(existing)

				if err := os.WriteFile(target, []byte(result), 0644); err != nil {
					return fmt.Errorf("write %s: %w", target, err)
				}

				base := filepath.Base(target)
				if inserted {
					fmt.Printf("tt: injected tt section into %s\n", base)
				} else {
					fmt.Printf("tt: updated tt section in %s\n", base)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&filePath, "file", "", "Target file (overrides default behaviour)")
	return cmd
}

func injectSection(content string) (result string, inserted bool) {
	lines := strings.Split(content, "\n")

	beginIdx := -1
	endIdx := -1
	for i, line := range lines {
		if line == sectionHeader {
			beginIdx = i
		} else if line == sectionEnd {
			endIdx = i
		}
	}

	if beginIdx == -1 {
		// Append
		sep := ""
		if content != "" && !strings.HasSuffix(content, "\n") {
			sep = "\n"
		}
		if content != "" {
			sep += "\n"
		}
		return content + sep + injectedContent + "\n", true
	}

	// Replace from header to end marker (or end of file if end marker missing)
	replaceEnd := endIdx
	if replaceEnd == -1 || replaceEnd < beginIdx {
		replaceEnd = len(lines) - 1
	}

	before := lines[:beginIdx]
	after := lines[replaceEnd+1:]

	var sb strings.Builder
	if len(before) > 0 {
		sb.WriteString(strings.Join(before, "\n"))
		sb.WriteByte('\n')
	}
	sb.WriteString(injectedContent)
	sb.WriteByte('\n')
	if len(after) > 0 {
		sb.WriteString(strings.Join(after, "\n"))
	}
	return sb.String(), false
}

// resolveTargetFiles returns all agent instruction files that exist at the git
// root. If neither exists, defaults to creating AGENTS.md. Both may be returned
// when both exist, since Claude reads CLAUDE.md and other agents read AGENTS.md.
func resolveTargetFiles() []string {
	root := gitRoot()
	if root == "" {
		root = "."
	}
	var found []string
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		p := filepath.Join(root, name)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if name == "CLAUDE.md" {
			if data, err := os.ReadFile(p); err == nil && strings.Contains(string(data), "@AGENTS.md") {
				continue
			}
		}
		found = append(found, p)
	}
	if len(found) == 0 {
		return []string{filepath.Join(root, "AGENTS.md")}
	}
	return found
}

func gitRoot() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
