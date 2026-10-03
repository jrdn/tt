package cmd

import (
	"fmt"
	"net"
	"net/http"

	"github.com/jrdn/tt/internal/api"
	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
)

func newWebCmd() *cobra.Command {
	var port int
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Start a local web UI",
		RunE: func(cmd *cobra.Command, args []string) error {
			srv := &api.Server{Resolve: func(*http.Request) (task.Store, error) { return store, nil }}
			mux := http.NewServeMux()
			mux.HandleFunc("GET /{$}", api.Index)
			srv.Register(mux, "/api")

			addr := fmt.Sprintf(":%d", port)
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			fmt.Printf("tt web: http://0.0.0.0%s\n", addr)
			return http.Serve(ln, mux)
		},
	}
	cmd.Flags().IntVarP(&port, "port", "p", 8080, "Port to listen on")
	return cmd
}
