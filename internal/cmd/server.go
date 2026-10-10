package cmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/jrdn/tt/internal/server"
	"github.com/jrdn/tt/internal/telemetry"
)

func newServerCmd() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Run the multiuser tt server (Postgres)",
		Long: `Run the multiuser tt server against Postgres.

Configuration comes from the environment:
  TT_DATABASE_URL       Postgres URL (required)
  TT_MASTER_KEY         base64 secret, at least 32 bytes (required);
                        generate with: openssl rand -base64 32
  TT_BASE_URL           public URL, for the GitHub callback (default http://localhost:8080)
  TT_GITHUB_CLIENT_ID, TT_GITHUB_CLIENT_SECRET
                        GitHub OAuth app; login is disabled without them
  TT_ADMINS             comma-separated GitHub logins made server admins
  TT_JWT_TTL            JWT lifetime (default 1h)
  TT_KEY_DEFAULT_TTL    API key lifetime when none is given (default 8760h)
  TT_KEY_MAX_TTL        longest API key lifetime allowed (default: the default)
  TT_LOGIN_TTL          lifetime of keys issued by tt login (default 2160h)

Observability: Prometheus metrics are served at /metrics on the same address
as the API. Logs are JSON on stderr. OTEL_SERVICE_NAME and
OTEL_RESOURCE_ATTRIBUTES set the resource attributes.`,
		Annotations: map[string]string{noLocalDB: "1"},
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			tel, err := telemetry.Setup(ctx)
			if err != nil {
				return err
			}
			defer func() {
				flush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				tel.Shutdown(flush)
			}()
			srv, err := openServer(ctx)
			if err != nil {
				return err
			}
			defer srv.Close()

			mux := http.NewServeMux()
			mux.Handle("GET /metrics", tel.MetricsHandler())
			mux.Handle("/", srv.Handler())
			hs := &http.Server{Addr: addr, Handler: tel.Middleware(mux), ReadHeaderTimeout: 10 * time.Second}
			go func() {
				<-ctx.Done()
				shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				hs.Shutdown(shutdown)
			}()
			slog.Info("tt server listening", "addr", addr)
			if err := hs.ListenAndServe(); err != http.ErrServerClosed {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&addr, "addr", ":8080", "Address to listen on")
	cmd.AddCommand(newServerAddUserCmd(), newServerLinkGitHubCmd(), newServerCreateKeyCmd(), newServerImportCmd())
	return cmd
}

func newServerAddUserCmd() *cobra.Command {
	var admin bool
	var github string
	cmd := &cobra.Command{
		Use:   "add-user <handle>",
		Short: "Create a user directly in the database (bootstrap and local dev)",
		Long: `Create a user without GitHub, for bootstrapping a server or local development.

With --github, the first GitHub sign-in as that login uses this user (its keys,
projects and history) instead of creating a new account. Without it, the user
is never matched to a GitHub account; see also tt server link-github.`,
		Example: `  tt server add-user jrdn --admin --github jrdn`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			srv, err := openServer(cmd.Context())
			if err != nil {
				return err
			}
			defer srv.Close()
			p, err := srv.Directory().CreateUser(cmd.Context(), args[0], nil, admin)
			if err != nil {
				return err
			}
			fmt.Printf("created user %s (%s)\n", p.Handle, p.ID)
			if github != "" {
				if err := srv.Directory().LinkGitHub(cmd.Context(), p.Handle, github); err != nil {
					return err
				}
				fmt.Printf("first GitHub sign-in as %s will use this user\n", github)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&admin, "admin", false, "Make the user a server admin")
	cmd.Flags().StringVar(&github, "github", "", "GitHub login that should sign in as this user")
	return cmd
}

func newServerLinkGitHubCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "link-github <handle> <github-login>",
		Short: "Let a GitHub account sign in as an existing user",
		Long: `Arrange for the first GitHub sign-in as <github-login> to use the existing user
<handle>, keeping its keys, projects and history. Only for users who haven't
signed in with GitHub yet.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			srv, err := openServer(cmd.Context())
			if err != nil {
				return err
			}
			defer srv.Close()
			if err := srv.Directory().LinkGitHub(cmd.Context(), args[0], args[1]); err != nil {
				return err
			}
			fmt.Printf("first GitHub sign-in as %s will use user %s\n", args[1], args[0])
			return nil
		},
	}
}

func newServerCreateKeyCmd() *cobra.Command {
	var expires time.Duration
	cmd := &cobra.Command{
		Use:   "create-key <handle>",
		Short: "Mint an API key for a user directly in the database (bootstrap)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			srv, err := openServer(cmd.Context())
			if err != nil {
				return err
			}
			defer srv.Close()
			dir := srv.Directory()
			p, err := dir.PrincipalByHandle(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			_, key, err := dir.CreateKey(cmd.Context(), p, p, "bootstrap", nil, "", time.Now().Add(expires))
			if err != nil {
				return err
			}
			fmt.Println(key)
			return nil
		},
	}
	cmd.Flags().DurationVar(&expires, "expires", 30*24*time.Hour, "Key lifetime")
	return cmd
}

func newServerImportCmd() *cobra.Command {
	var project, as string
	cmd := &cobra.Command{
		Use:   "import <sqlite-db>",
		Short: "Copy a local tt database into a server project",
		Long: `Copy a local tt SQLite database into a server project, keeping task IDs,
timestamps and author handles. Rows already present are skipped, so the
import can be re-run. The project must exist.`,
		Example: `  tt server import --project tt --as jrdn ~/.config/tt/tt.db`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if project == "" || as == "" {
				return fmt.Errorf("--project and --as are required")
			}
			srv, err := openServer(cmd.Context())
			if err != nil {
				return err
			}
			defer srv.Close()
			dir := srv.Directory()
			p, err := dir.ProjectBySlug(cmd.Context(), project)
			if err != nil {
				return err
			}
			actor, err := dir.PrincipalByHandle(cmd.Context(), as)
			if err != nil {
				return err
			}
			st, err := dir.ImportSQLite(cmd.Context(), p, args[0], actor)
			if err != nil {
				return err
			}
			fmt.Printf("imported %d tasks, %d comments, %d relations, %d external refs into %s\n",
				st.Tasks, st.Comments, st.Relations, st.ExternalRefs, project)
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Destination project slug")
	cmd.Flags().StringVar(&as, "as", "", "Handle recorded as the importer in the audit log")
	return cmd
}

// openServer builds a server from TT_* environment variables.
func openServer(ctx context.Context) (*server.Server, error) {
	cfg := server.Config{
		DatabaseURL:        os.Getenv("TT_DATABASE_URL"),
		BaseURL:            envOr("TT_BASE_URL", "http://localhost:8080"),
		GitHubClientID:     os.Getenv("TT_GITHUB_CLIENT_ID"),
		GitHubClientSecret: os.Getenv("TT_GITHUB_CLIENT_SECRET"),
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("TT_DATABASE_URL is required")
	}
	mk := os.Getenv("TT_MASTER_KEY")
	if mk == "" {
		return nil, fmt.Errorf("TT_MASTER_KEY is required (generate with: openssl rand -base64 32)")
	}
	var err error
	if cfg.MasterKey, err = base64.StdEncoding.DecodeString(mk); err != nil {
		return nil, fmt.Errorf("TT_MASTER_KEY must be base64: %w", err)
	}
	if a := os.Getenv("TT_ADMINS"); a != "" {
		cfg.AdminLogins = strings.Split(a, ",")
	}
	for env, dst := range map[string]*time.Duration{
		"TT_JWT_TTL": &cfg.JWTTTL, "TT_KEY_DEFAULT_TTL": &cfg.KeyDefaultTTL, "TT_KEY_MAX_TTL": &cfg.KeyMaxTTL,
		"TT_LOGIN_TTL": &cfg.LoginTTL,
	} {
		if v := os.Getenv(env); v != "" {
			if *dst, err = time.ParseDuration(v); err != nil {
				return nil, fmt.Errorf("%s: %w", env, err)
			}
		}
	}
	return server.New(ctx, cfg)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
