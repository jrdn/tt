package api

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jrdn/tt/internal/task"
	"github.com/yuin/goldmark"
)

//go:embed web_ui.html
var webUI string

// Server serves the task JSON API. Resolve picks the store for a request:
// the local database for tt web, or the project's authorized store on the
// server.
type Server struct {
	Resolve func(r *http.Request) (task.Store, error)
}

// Register mounts the task API under prefix, e.g. "/api" or
// "/api/v1/projects/{project}".
func (s *Server) Register(mux *http.ServeMux, prefix string) {
	mux.HandleFunc("GET "+prefix+"/search", s.handleSearch)
	mux.HandleFunc("GET "+prefix+"/tasks/recent", s.handleRecentTasks)
	mux.HandleFunc("POST "+prefix+"/tasks", s.handleCreateTask)
	mux.HandleFunc("GET "+prefix+"/tasks", s.handleListTasks)
	mux.HandleFunc("GET "+prefix+"/tasks/{id}", s.handleGetTask)
	mux.HandleFunc("PATCH "+prefix+"/tasks/{id}", s.handleUpdateTask)
	mux.HandleFunc("POST "+prefix+"/tasks/{id}/comments", s.handleAddComment)
	mux.HandleFunc("POST "+prefix+"/tasks/{id}/relations", s.handleAddRelation)
	mux.HandleFunc("DELETE "+prefix+"/tasks/{from}/relations/{rtype}/{to}", s.handleRemoveRelation)
}

// ErrForbidden is returned (wrapped) by stores that deny an action; the API
// maps it to 403.
var ErrForbidden = errors.New("forbidden")

// ErrNotFound is returned (wrapped) by Resolve for unknown projects.
var ErrNotFound = errors.New("not found")

func (s *Server) store(w http.ResponseWriter, r *http.Request) (task.Store, bool) {
	st, err := s.Resolve(r)
	if err != nil {
		writeStoreErr(w, 500, err)
		return nil, false
	}
	return st, true
}

// writeStoreErr writes err with code, unless it is a permission or lookup
// failure, which get 403 or 404.
func writeStoreErr(w http.ResponseWriter, code int, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		code = http.StatusForbidden
	case errors.Is(err, ErrNotFound):
		code = http.StatusNotFound
	}
	writeErr(w, code, err.Error())
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

// Index serves the single-page web UI.
func Index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, webUI)
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	opts := task.ListOpts{
		Status:   q.Get("status"),
		Assignee: q.Get("assignee"),
		All:      q.Get("all") == "1",
		Ready:    q.Get("ready") == "1",
	}
	tasks, err := st.List(r.Context(), opts)
	if err != nil {
		writeStoreErr(w, 500, err)
		return
	}
	writeJSON(w, map[string]any{"tasks": tasks})
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	t, err := st.Get(r.Context(), id)
	if err != nil {
		writeStoreErr(w, 404, err)
		return
	}
	subtasks, _ := st.List(r.Context(), task.ListOpts{ParentID: t.ID, All: true})
	if subtasks == nil {
		subtasks = []task.Task{}
	}
	rels, _ := st.GetRelations(r.Context(), t.ID)
	if rels == nil {
		rels = []task.Relation{}
	}
	rawComments, _ := st.GetComments(r.Context(), t.ID)

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

func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeStoreErr(w, 400, err)
		return
	}
	t, err := st.Get(r.Context(), id)
	if err != nil {
		writeStoreErr(w, 404, err)
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
	if err := st.Save(r.Context(), t); err != nil {
		writeStoreErr(w, 500, err)
		return
	}
	writeJSON(w, t)
}

func (s *Server) handleAddComment(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var body struct {
		Body   string  `json:"body"`
		Author *string `json:"author"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeStoreErr(w, 400, err)
		return
	}
	if strings.TrimSpace(body.Body) == "" {
		writeErr(w, 400, "body is required")
		return
	}
	c, err := st.AddComment(r.Context(), id, body.Body, body.Author)
	if err != nil {
		writeStoreErr(w, 500, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, webCommentDetail{Comment: *c, BodyHTML: mdToHTML(c.Body)})
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store(w, r)
	if !ok {
		return
	}
	var body struct {
		Title       string  `json:"title"`
		Description *string `json:"description"`
		Priority    *int    `json:"priority"`
		Assignee    *string `json:"assignee"`
		Status      *string `json:"status"`
		ParentID    *string `json:"parent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeStoreErr(w, 400, err)
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
	t, err := st.Create(r.Context(), body.Title, opts)
	if err != nil {
		writeStoreErr(w, 500, err)
		return
	}
	// task.Create always sets status=open; apply a different status if requested
	if body.Status != nil && *body.Status != string(task.StatusOpen) {
		status := task.Status(*body.Status)
		t, err = st.Update(r.Context(), t.ID, task.UpdateOpts{Status: &status})
		if err != nil {
			writeStoreErr(w, 500, err)
			return
		}
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, t)
}

func (s *Server) handleAddRelation(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var body struct {
		Type     string `json:"type"`
		TargetID string `json:"target_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeStoreErr(w, 400, err)
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
	if err := st.AddRelation(r.Context(), id, body.Type, body.TargetID); err != nil {
		writeStoreErr(w, 500, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]string{"ok": "1"})
}

func (s *Server) handleRemoveRelation(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store(w, r)
	if !ok {
		return
	}
	from := r.PathValue("from")
	rtype := r.PathValue("rtype")
	to := r.PathValue("to")
	if err := st.RemoveRelation(r.Context(), from, rtype, to); err != nil {
		writeStoreErr(w, 500, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store(w, r)
	if !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, map[string]any{"tasks": []task.Task{}})
		return
	}
	tasks, err := st.Search(r.Context(), q)
	if err != nil {
		writeStoreErr(w, 500, err)
		return
	}
	if tasks == nil {
		tasks = []task.Task{}
	}
	writeJSON(w, map[string]any{"tasks": tasks})
}

func (s *Server) handleRecentTasks(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store(w, r)
	if !ok {
		return
	}
	tasks, err := st.Recent(r.Context(), 5)
	if err != nil {
		writeStoreErr(w, 500, err)
		return
	}
	if tasks == nil {
		tasks = []task.Task{}
	}
	writeJSON(w, map[string]any{"tasks": tasks})
}
