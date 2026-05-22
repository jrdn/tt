package cmd

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/jrdn/tt/internal/task"
	"github.com/spf13/cobra"
	"github.com/yuin/goldmark"
)

//go:embed web_ui.html
var webUI string

type webServer struct {
	db *sqlx.DB
}

func mdToHTML(s string) string {
	var buf bytes.Buffer
	if err := goldmark.Convert([]byte(s), &buf); err != nil {
		return s
	}
	return buf.String()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

type webCommentDetail struct {
	task.Comment
	BodyHTML string `json:"body_html"`
}

type webTaskDetail struct {
	Task            *task.Task         `json:"task"`
	Subtasks        []task.Task        `json:"subtasks"`
	Relations       []task.Relation    `json:"relations"`
	Comments        []webCommentDetail `json:"comments"`
	DescriptionHTML string             `json:"description_html"`
}

func (s *webServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, webUI)
}

func (s *webServer) handleListTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	opts := task.ListOpts{
		Status:   q.Get("status"),
		Assignee: q.Get("assignee"),
		All:      q.Get("all") == "1",
		Ready:    q.Get("ready") == "1",
	}
	tasks, err := task.List(s.db, opts)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"tasks": tasks})
}

func (s *webServer) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := task.Get(s.db, id)
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	subtasks, _ := task.List(s.db, task.ListOpts{ParentID: t.ID, All: true})
	rels, _ := task.GetRelations(s.db, t.ID)
	rawComments, _ := task.GetComments(s.db, t.ID)

	comments := make([]webCommentDetail, len(rawComments))
	for i, c := range rawComments {
		comments[i] = webCommentDetail{Comment: c, BodyHTML: mdToHTML(c.Body)}
	}

	var descHTML string
	if t.Description != nil && *t.Description != "" {
		descHTML = mdToHTML(*t.Description)
	}

	writeJSON(w, webTaskDetail{
		Task:            t,
		Subtasks:        subtasks,
		Relations:       rels,
		Comments:        comments,
		DescriptionHTML: descHTML,
	})
}

func (s *webServer) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Status   *string `json:"status"`
		Assignee *string `json:"assignee"`
		Title    *string `json:"title"`
		Priority *int    `json:"priority"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	opts := task.UpdateOpts{}
	if body.Status != nil {
		st := task.Status(*body.Status)
		opts.Status = &st
	}
	if body.Title != nil {
		opts.Title = body.Title
	}
	if body.Priority != nil {
		opts.Priority = body.Priority
	}
	if body.Assignee != nil && *body.Assignee != "" {
		opts.Assignee = body.Assignee
	}
	t, err := task.Update(s.db, id, opts)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, t)
}

func (s *webServer) handleAddComment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Body   string  `json:"body"`
		Author *string `json:"author"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(body.Body) == "" {
		writeErr(w, 400, "body is required")
		return
	}
	c, err := task.AddComment(s.db, id, body.Body, body.Author)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, webCommentDetail{Comment: *c, BodyHTML: mdToHTML(c.Body)})
}

func newWebCmd() *cobra.Command {
	var port int
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Start a local web UI",
		RunE: func(cmd *cobra.Command, args []string) error {
			srv := &webServer{db: db}
			mux := http.NewServeMux()
			mux.HandleFunc("GET /{$}", srv.handleIndex)
			mux.HandleFunc("GET /api/tasks", srv.handleListTasks)
			mux.HandleFunc("GET /api/tasks/{id}", srv.handleGetTask)
			mux.HandleFunc("PATCH /api/tasks/{id}", srv.handleUpdateTask)
			mux.HandleFunc("POST /api/tasks/{id}/comments", srv.handleAddComment)

			addr := fmt.Sprintf("localhost:%d", port)
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			fmt.Printf("tt web: http://%s\n", addr)
			return http.Serve(ln, mux)
		},
	}
	cmd.Flags().IntVarP(&port, "port", "p", 8080, "Port to listen on")
	return cmd
}
