package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jrdn/tt/internal/auth"
)

func newKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "key",
		Short:       "Work with tt server API keys",
		Annotations: map[string]string{noLocalDB: "1"},
	}
	cmd.AddCommand(newKeyAttenuateCmd())
	addServerKeyCmds(cmd)
	return cmd
}

func newKeyAttenuateCmd() *cobra.Command {
	var (
		key, role, label string
		projects, tasks  []string
		actions          []string
		expires          time.Duration
	)
	cmd := &cobra.Command{
		Use:   "attenuate",
		Short: "Derive a narrower API key for a sub-agent, offline",
		Long: `Derive a new API key that can do at most what the parent key can, further
limited by the given flags. Runs offline: no call to the server.

The parent key comes from --key or TT_API_KEY. Task IDs must be full IDs.

Actions: ` + actionList(),
		Example: `  tt key attenuate --task ab12cde --actions task:read,comment:create --expires 2h --label sub-reviewer`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if key == "" {
				key = os.Getenv("TT_API_KEY")
			}
			if key == "" {
				return fmt.Errorf("no parent key: pass --key or set TT_API_KEY")
			}
			r := auth.Restrictions{Tasks: tasks, Label: label}
			if cmd.Flags().Changed("project") {
				r.Projects = projects
			}
			if role != "" {
				var err error
				if r.MaxRole, err = auth.ParseRole(role); err != nil {
					return err
				}
			}
			for _, a := range actions {
				act, err := auth.ParseAction(a)
				if err != nil {
					return err
				}
				r.Actions = append(r.Actions, act)
			}
			if expires > 0 {
				r.Expires = time.Now().Add(expires)
			}
			sub, err := auth.Attenuate(key, r)
			if err != nil {
				return err
			}
			fmt.Println(sub)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&key, "key", "", "Parent API key (default $TT_API_KEY)")
	f.StringSliceVar(&projects, "project", nil, "Allow only these projects")
	f.StringVar(&role, "role", "", "Cap the role: viewer, member or owner")
	f.StringSliceVar(&tasks, "task", nil, "Allow writes only under this task (repeatable)")
	f.StringSliceVar(&actions, "actions", nil, "Allow only these actions")
	f.DurationVar(&expires, "expires", 0, "Expire after this long, e.g. 2h")
	f.StringVar(&label, "label", "", "Sub-agent label shown in attribution")
	return cmd
}

func actionList() string {
	var s []string
	for _, a := range auth.RoleActions[auth.RoleOwner] {
		s = append(s, string(a))
	}
	return strings.Join(s, ", ")
}
