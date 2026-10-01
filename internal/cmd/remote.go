package cmd

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/jrdn/tt/internal/auth"
	"github.com/jrdn/tt/internal/client"
	"github.com/jrdn/tt/internal/task"
)

// serverFor picks the server for account commands: --server, TT_SERVER,
// the repo's .tt.json, then the default set by the last tt login.
func serverFor(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if s := os.Getenv("TT_SERVER"); s != "" {
		return s, nil
	}
	cfg, ok, err := client.FindProject()
	if err != nil {
		return "", err
	}
	if ok {
		return cfg.Server, nil
	}
	def, err := client.DefaultServer()
	if err != nil {
		return "", err
	}
	if def == "" {
		return "", fmt.Errorf("no tt server configured: run tt login --server <url> (it becomes your default), " +
			"or tt init --server <url> --project <slug> in a repo")
	}
	return def, nil
}

// serverClient returns an authenticated client for account commands.
func serverClient(flag string) (*client.Client, error) {
	server, err := serverFor(flag)
	if err != nil {
		return nil, err
	}
	key, err := client.KeyFor(server)
	if err != nil {
		return nil, err
	}
	return client.New(server, key), nil
}

// accountCmd builds a command that talks to the server directly rather
// than through a project store.
func accountCmd(c *cobra.Command, server *string) *cobra.Command {
	c.Annotations = map[string]string{noLocalDB: "1"}
	c.PersistentFlags().StringVar(server, "server", "", "tt server URL (default: from .tt.json or TT_SERVER)")
	return c
}

func newLoginCmd() *cobra.Command {
	var server, key string
	var device, noBrowser bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to a tt server",
		Long: `Log in to a tt server. By default this opens your browser; sign in with GitHub
if asked and approve. The CLI then receives an API key (90 days by default)
and stores it in the OS keychain (macOS Keychain, Secret Service on Linux,
Windows Credential Manager). Without a keychain, or with TT_KEYRING=off, it
goes in credentials.json in the tt config directory (mode 0600).

With --device, log in from a machine without a browser (e.g. over SSH): open
the URL shown on any device and type the code.

With --key, store an existing API key instead (e.g. one from tt server create-key).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			srv, err := serverFor(server)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			switch {
			case key != "":
			case device:
				key, err = deviceLogin(ctx, client.New(srv, ""))
			default:
				key, err = loopbackLogin(ctx, client.New(srv, ""), !noBrowser)
			}
			if err != nil {
				return err
			}
			var who auth.Claims
			if err := client.New(srv, key).Do(ctx, "GET", "/api/v1/whoami", nil, &who); err != nil {
				return fmt.Errorf("check key: %w", err)
			}
			where, err := client.SaveCredential(srv, client.Credential{Key: key, Handle: who.Handle})
			if err != nil {
				return err
			}
			if err := client.SetDefaultServer(srv); err != nil {
				return err
			}
			fmt.Printf("Logged in to %s as %s (key stored in %s). It's now your default server.\n", srv, who.Handle, where)
			return nil
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "Store this API key instead of logging in with a browser")
	cmd.Flags().BoolVar(&device, "device", false, "Log in from a machine without a browser: type a code on another device")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Print the login URL instead of opening a browser")
	return accountCmd(cmd, &server)
}

// loopbackLogin runs the default browser login (RFC 8252 with PKCE): the
// server redirects a one-time code to a listener on 127.0.0.1, and only this
// process, holding the PKCE verifier, can redeem it.
func loopbackLogin(ctx context.Context, c *client.Client, openBrowser bool) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("start login listener: %w (try tt login --device)", err)
	}
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)
	state, verifier := randomToken(16), randomToken(32)
	sum := sha256.Sum256([]byte(verifier))
	host, _ := os.Hostname()
	authURL := c.Server + "/auth/cli?" + url.Values{
		"redirect_uri":          {redirect},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		"client_name":           {host},
	}.Encode()

	codes := make(chan string, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/callback" || q.Get("state") != state || q.Get("code") == "" {
			http.Error(w, "not a tt login response", http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, "<!doctype html><title>tt login</title><p>Logged in to tt. You can close this tab.</p>")
		select {
		case codes <- q.Get("code"):
		default:
		}
	})}
	go srv.Serve(ln)
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()

	if openBrowser && openURL(authURL) == nil {
		fmt.Printf("Opening your browser to log in. If it doesn't open, visit:\n\n  %s\n\n", authURL)
	} else {
		fmt.Printf("Open this URL in a browser on this machine to log in:\n\n  %s\n\n", authURL)
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(5 * time.Minute):
		return "", fmt.Errorf("login timed out; run tt login again")
	case code := <-codes:
		var resp struct {
			Key string `json:"key"`
		}
		err := c.Anonymous(ctx, "POST", "/auth/cli/token",
			map[string]string{"code": code, "code_verifier": verifier, "redirect_uri": redirect}, &resp)
		return resp.Key, err
	}
}

func randomToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// openURL opens u in the default browser.
func openURL(u string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", u).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	default:
		return exec.Command("xdg-open", u).Start()
	}
}

// deviceLogin logs in from a machine without a browser: the user types the
// code on another device, then the poll returns the issued key.
func deviceLogin(ctx context.Context, c *client.Client) (string, error) {
	host, _ := os.Hostname()
	var start struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		URI        string `json:"verification_uri"`
		ExpiresIn  int    `json:"expires_in"`
		Interval   int    `json:"interval"`
	}
	if err := c.Anonymous(ctx, "POST", "/auth/device/start", map[string]string{"client_name": host}, &start); err != nil {
		return "", err
	}
	fmt.Printf("On any device, open:\n\n  %s\n\nand enter the code:\n\n  %s\n\nWaiting for approval...\n",
		start.URI, start.UserCode)
	deadline := time.Now().Add(time.Duration(start.ExpiresIn) * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Duration(start.Interval) * time.Second):
		}
		var resp struct {
			Key string `json:"key"`
		}
		err := c.Anonymous(ctx, "POST", "/auth/device/token", map[string]string{"device_code": start.DeviceCode}, &resp)
		var ae *client.APIError
		if errors.As(err, &ae) && ae.Message == "authorization_pending" {
			continue
		}
		if err != nil {
			return "", err
		}
		return resp.Key, nil
	}
	return "", fmt.Errorf("login timed out; run tt login again")
}

func newLogoutCmd() *cobra.Command {
	var server string
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Revoke and forget the stored login for a tt server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			srv, err := serverFor(server)
			if err != nil {
				return err
			}
			if key, err := client.KeyFor(srv); err == nil && os.Getenv("TT_API_KEY") == "" {
				if id, err := auth.KeyID(key); err == nil {
					if err := client.New(srv, key).Do(cmd.Context(), "DELETE", "/api/v1/keys/"+id, nil, nil); err != nil {
						fmt.Fprintf(os.Stderr, "tt: could not revoke key on server: %v\n", err)
					}
				}
			}
			if _, err := client.SaveCredential(srv, client.Credential{}); err != nil {
				return err
			}
			if def, _ := client.DefaultServer(); def == strings.TrimSuffix(srv, "/") {
				if err := client.SetDefaultServer(""); err != nil {
					return err
				}
			}
			fmt.Printf("Logged out of %s\n", srv)
			return nil
		},
	}
	return accountCmd(cmd, &server)
}

func newWhoamiCmd() *cobra.Command {
	var server string
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show who you are on the tt server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := serverClient(server)
			if err != nil {
				return err
			}
			var who auth.Claims
			if err := c.Do(cmd.Context(), "GET", "/api/v1/whoami", nil, &who); err != nil {
				return err
			}
			if jsonOutput {
				return printJSON(who)
			}
			line := fmt.Sprintf("%s (%s)", who.Handle, who.Kind)
			if who.OnBehalfOf != "" {
				line += " on behalf of " + who.OnBehalfOf
			}
			if who.Label != "" {
				line += ", label " + who.Label
			}
			if who.Admin {
				line += ", server admin"
			}
			fmt.Printf("%s on %s\n", line, c.Server)
			slugs := make([]string, 0, len(who.Projects))
			for s := range who.Projects {
				slugs = append(slugs, s)
			}
			sort.Strings(slugs)
			for _, s := range slugs {
				fmt.Printf("  %-20s %s\n", s, who.Projects[s])
			}
			if who.Actions != nil {
				fmt.Printf("  actions limited to: %v\n", who.Actions)
			}
			if who.TaskScope != nil {
				fmt.Printf("  writes limited to tasks under: %v\n", who.TaskScope)
			}
			return nil
		},
	}
	return accountCmd(cmd, &server)
}

func newAgentCmd() *cobra.Command {
	var server string
	cmd := accountCmd(&cobra.Command{Use: "agent", Short: "Manage your agents on the tt server"}, &server)
	cmd.AddCommand(&cobra.Command{
		Use:     "add <handle>",
		Short:   "Register an agent you own, e.g. claude/opus4.7",
		Example: "  tt agent add claude/opus4.7\n  tt key create --agent claude/opus4.7 --role member",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := serverClient(server)
			if err != nil {
				return err
			}
			if err := c.Do(cmd.Context(), "POST", "/api/v1/agents", map[string]string{"handle": args[0]}, nil); err != nil {
				return err
			}
			fmt.Printf("added agent %s\n", args[0])
			return nil
		},
	})
	return cmd
}

func newProjectCmd() *cobra.Command {
	var server string
	cmd := accountCmd(&cobra.Command{Use: "project", Short: "Manage projects on the tt server"}, &server)

	var name string
	create := &cobra.Command{
		Use:   "create <slug>",
		Short: "Create a project (server admins)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := serverClient(server)
			if err != nil {
				return err
			}
			if err := c.Do(cmd.Context(), "POST", "/api/v1/projects",
				map[string]string{"slug": args[0], "name": name}, nil); err != nil {
				return err
			}
			fmt.Printf("created project %s\n", args[0])
			return nil
		},
	}
	create.Flags().StringVar(&name, "name", "", "Display name (default: the slug)")

	ls := &cobra.Command{
		Use:   "ls",
		Short: "List projects you can access",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := serverClient(server)
			if err != nil {
				return err
			}
			var resp struct {
				Projects []struct {
					Slug string `json:"slug"`
					Name string `json:"name"`
					Role string `json:"role"`
				} `json:"projects"`
			}
			if err := c.Do(cmd.Context(), "GET", "/api/v1/projects", nil, &resp); err != nil {
				return err
			}
			if jsonOutput {
				return printJSON(resp.Projects)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "SLUG\tNAME\tROLE")
			for _, p := range resp.Projects {
				fmt.Fprintf(w, "%s\t%s\t%s\n", p.Slug, p.Name, p.Role)
			}
			return w.Flush()
		},
	}

	var remove bool
	member := &cobra.Command{
		Use:     "member <slug> <handle> [viewer|member|owner]",
		Short:   "Add, change or remove a project member (project owners)",
		Example: "  tt project member tt alice member\n  tt project member tt alice --remove",
		Args:    cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := serverClient(server)
			if err != nil {
				return err
			}
			path := "/api/v1/projects/" + args[0] + "/members/" + args[1]
			if remove {
				if err := c.Do(cmd.Context(), "DELETE", path, nil, nil); err != nil {
					return err
				}
				fmt.Printf("removed %s from %s\n", args[1], args[0])
				return nil
			}
			if len(args) != 3 {
				return fmt.Errorf("give a role (viewer, member or owner) or --remove")
			}
			if err := c.Do(cmd.Context(), "PUT", path, map[string]string{"role": args[2]}, nil); err != nil {
				return err
			}
			fmt.Printf("%s is now %s on %s\n", args[1], args[2], args[0])
			return nil
		},
	}
	member.Flags().BoolVar(&remove, "remove", false, "Remove the member")

	cmd.AddCommand(create, ls, member)
	return cmd
}

// addServerKeyCmds adds the server-backed subcommands to `tt key`.
func addServerKeyCmds(cmd *cobra.Command) {
	var server string
	cmd.PersistentFlags().StringVar(&server, "server", "", "tt server URL (default: from .tt.json or TT_SERVER)")

	var agent, role, name string
	var projects []string
	var expires time.Duration
	create := &cobra.Command{
		Use:   "create",
		Short: "Mint an API key for you or one of your agents",
		Long: `Mint an API key for you or one of your agents. The key is printed once, to
stdout. Give it to the agent as TT_API_KEY.`,
		Example: "  tt key create --agent claude/opus4.7 --project tt --role member --expires 720h",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := serverClient(server)
			if err != nil {
				return err
			}
			body := map[string]any{"agent": agent, "name": name, "role": role}
			if cmd.Flags().Changed("project") {
				body["projects"] = projects
			}
			if expires > 0 {
				body["expires"] = expires.String()
			}
			var resp struct {
				Key string `json:"key"`
			}
			if err := c.Do(cmd.Context(), "POST", "/api/v1/keys", body, &resp); err != nil {
				return err
			}
			fmt.Println(resp.Key)
			return nil
		},
	}
	create.Flags().StringVar(&agent, "agent", "", "Agent handle (default: a key for yourself)")
	create.Flags().StringSliceVar(&projects, "project", nil, "Limit to these projects")
	create.Flags().StringVar(&role, "role", "", "Cap the role: viewer, member or owner (default member for agents)")
	create.Flags().DurationVar(&expires, "expires", 0, "Lifetime (default: server default)")
	create.Flags().StringVar(&name, "name", "", "Label to recognise the key by")

	ls := &cobra.Command{
		Use:   "ls",
		Short: "List keys for you and your agents",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := serverClient(server)
			if err != nil {
				return err
			}
			var resp struct {
				Keys []struct {
					ID         string     `json:"id"`
					Handle     string     `json:"handle"`
					Name       string     `json:"name"`
					Projects   []string   `json:"projects"`
					MaxRole    *string    `json:"max_role"`
					ExpiresAt  *time.Time `json:"expires_at"`
					LastUsedAt *time.Time `json:"last_used_at"`
					RevokedAt  *time.Time `json:"revoked_at"`
				} `json:"keys"`
			}
			if err := c.Do(cmd.Context(), "GET", "/api/v1/keys", nil, &resp); err != nil {
				return err
			}
			if jsonOutput {
				return printJSON(resp.Keys)
			}
			day := func(t *time.Time) string {
				if t == nil {
					return "-"
				}
				return t.Local().Format("2006-01-02")
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tHANDLE\tNAME\tPROJECTS\tROLE\tEXPIRES\tLAST USED\tSTATUS")
			for _, k := range resp.Keys {
				projs, role, status := "all", "-", "active"
				if k.Projects != nil {
					projs = strings.Join(k.Projects, ",")
				}
				if k.MaxRole != nil {
					role = *k.MaxRole
				}
				if k.RevokedAt != nil {
					status = "revoked"
				} else if k.ExpiresAt != nil && k.ExpiresAt.Before(time.Now()) {
					status = "expired"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", k.ID, k.Handle, k.Name, projs, role,
					day(k.ExpiresAt), day(k.LastUsedAt), status)
			}
			return w.Flush()
		},
	}

	revoke := &cobra.Command{
		Use:   "revoke <key-id>",
		Short: "Revoke a key (and every key derived from it)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := serverClient(server)
			if err != nil {
				return err
			}
			if err := c.Do(cmd.Context(), "DELETE", "/api/v1/keys/"+args[0], nil, nil); err != nil {
				return err
			}
			fmt.Printf("revoked %s\n", args[0])
			return nil
		},
	}
	cmd.AddCommand(create, ls, revoke)
}

func newExportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "export",
		Short: "Export every task with its comments and relations as JSON",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			tasks, err := store.List(ctx, task.ListOpts{All: true})
			if err != nil {
				return err
			}
			type entry struct {
				task.Task
				Comments  []task.Comment  `json:"comments"`
				Relations []task.Relation `json:"relations"`
			}
			out := make([]entry, 0, len(tasks))
			for _, t := range tasks {
				comments, err := store.GetComments(ctx, t.ID)
				if err != nil {
					return err
				}
				rels, err := store.GetRelations(ctx, t.ID)
				if err != nil {
					return err
				}
				// Each relation is listed on both ends; keep it on its source.
				own := []task.Relation{}
				for _, r := range rels {
					if r.FromID == t.ID {
						own = append(own, r)
					}
				}
				if comments == nil {
					comments = []task.Comment{}
				}
				out = append(out, entry{Task: t, Comments: comments, Relations: own})
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(map[string]any{"exported_at": time.Now().UTC().Format(time.RFC3339), "tasks": out})
		},
	}
}
