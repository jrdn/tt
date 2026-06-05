package cmd

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

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
	if subtasks == nil {
		subtasks = []task.Task{}
	}
	rels, _ := task.GetRelations(s.db, t.ID)
	if rels == nil {
		rels = []task.Relation{}
	}
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
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	t, err := task.Get(s.db, id)
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	str := func(v json.RawMessage) string {
		var s string
		json.Unmarshal(v, &s)
		return s
	}
	if v, ok := raw["status"]; ok {
		t.Status = task.Status(str(v))
		if t.Status == task.StatusDone || t.Status == task.StatusCancelled {
			ts := time.Now().UTC().Format(time.RFC3339)
			t.ClosedAt = &ts
		} else {
			t.ClosedAt = nil
		}
	}
	if v, ok := raw["title"]; ok {
		t.Title = str(v)
	}
	if v, ok := raw["description"]; ok {
		s := str(v)
		if s == "" {
			t.Description = nil
		} else {
			t.Description = &s
		}
	}
	if v, ok := raw["priority"]; ok {
		json.Unmarshal(v, &t.Priority)
	}
	if v, ok := raw["assignee"]; ok {
		s := str(v)
		if s == "" {
			t.Assignee = nil
		} else {
			t.Assignee = &s
		}
	}
	if v, ok := raw["due_date"]; ok {
		s := str(v)
		if s == "" {
			t.DueDate = nil
		} else {
			t.DueDate = &s
		}
	}
	t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := task.Save(s.db, t); err != nil {
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

func (s *webServer) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title       string  `json:"title"`
		Description *string `json:"description"`
		Priority    *int    `json:"priority"`
		Assignee    *string `json:"assignee"`
		Status      *string `json:"status"`
		ParentID    *string `json:"parent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(body.Title) == "" {
		writeErr(w, 400, "title is required")
		return
	}
	opts := task.CreateOpts{
		Description: body.Description,
		Assignee:    body.Assignee,
		ParentID:    body.ParentID,
	}
	if body.Priority != nil {
		opts.Priority = *body.Priority
		opts.PrioritySet = true
	}
	t, err := task.Create(s.db, body.Title, opts)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// task.Create always sets status=open; apply a different status if requested
	if body.Status != nil && *body.Status != string(task.StatusOpen) {
		st := task.Status(*body.Status)
		t, err = task.Update(s.db, t.ID, task.UpdateOpts{Status: &st})
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, t)
}

func (s *webServer) handleAddRelation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Type     string `json:"type"`
		TargetID string `json:"target_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	body.Type = strings.TrimSpace(body.Type)
	body.TargetID = strings.TrimSpace(body.TargetID)
	if body.Type == "" || body.TargetID == "" {
		writeErr(w, 400, "type and target_id are required")
		return
	}
	allowed := map[string]bool{"blocks": true, "duplicates": true, "related": true}
	if !allowed[body.Type] {
		writeErr(w, 400, "type must be one of: blocks, duplicates, related")
		return
	}
	if err := task.AddRelation(s.db, id, body.Type, body.TargetID); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]string{"ok": "1"})
}

func (s *webServer) handleRemoveRelation(w http.ResponseWriter, r *http.Request) {
	from  := r.PathValue("from")
	rtype := r.PathValue("rtype")
	to    := r.PathValue("to")
	if err := task.RemoveRelation(s.db, from, rtype, to); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *webServer) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, map[string]any{"tasks": []task.Task{}})
		return
	}
	tasks, err := task.Search(s.db, q)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if tasks == nil {
		tasks = []task.Task{}
	}
	writeJSON(w, map[string]any{"tasks": tasks})
}

func (s *webServer) handleRecentTasks(w http.ResponseWriter, r *http.Request) {
	var tasks []task.Task
	if err := s.db.Select(&tasks, `SELECT * FROM tasks ORDER BY updated_at DESC LIMIT 5`); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if tasks == nil {
		tasks = []task.Task{}
	}
	writeJSON(w, map[string]any{"tasks": tasks})
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
			mux.HandleFunc("GET /api/search", srv.handleSearch)
			mux.HandleFunc("GET /api/tasks/recent", srv.handleRecentTasks)
			mux.HandleFunc("POST /api/tasks", srv.handleCreateTask)
			mux.HandleFunc("GET /api/tasks", srv.handleListTasks)
			mux.HandleFunc("GET /api/tasks/{id}", srv.handleGetTask)
			mux.HandleFunc("PATCH /api/tasks/{id}", srv.handleUpdateTask)
			mux.HandleFunc("POST /api/tasks/{id}/comments", srv.handleAddComment)
			mux.HandleFunc("POST /api/tasks/{id}/relations", srv.handleAddRelation)
			mux.HandleFunc("DELETE /api/tasks/{from}/relations/{rtype}/{to}", srv.handleRemoveRelation)

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
