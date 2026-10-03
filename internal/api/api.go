package api

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
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
	mux.HandleFunc("POST "+prefix+"/next", s.handleNext)
	mux.HandleFunc("GET "+prefix+"/events", s.handleEvents)
	mux.HandleFunc("GET "+prefix+"/external-refs/{source}/{external_id}", s.handleFindExternalRef)
	mux.HandleFunc("PUT "+prefix+"/external-refs/{source}/{external_id}", s.handleUpsertExternalRef)
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
	ExternalRefs    []task.ExternalRef `json:"external_refs"`
	DescriptionHTML string             `json:"description_html"`
}

// ServerIndex serves the web UI in tt server mode: it logs in via the
// session cookie and talks to /api/v1/projects/{project}.
func ServerIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, strings.Replace(webUI, "/*TT_CONFIG*/null", "true", 1))
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
		ParentID: q.Get("parent"),
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
	refs, _ := st.GetExternalRefs(r.Context(), t.ID)
	if refs == nil {
		refs = []task.ExternalRef{}
	}

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
		ExternalRefs:    refs,
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
	if v, ok := raw["parent_id"]; ok {
		s := str(v)
		if s == "" {
			t.ParentID = nil
		} else {
			p, err := st.Get(r.Context(), s)
			if err != nil {
				writeErr(w, 400, fmt.Sprintf("parent task %q not found", s))
				return
			}
			if p.ID == t.ID {
				writeErr(w, 400, "a task can't be its own parent")
				return
			}
			t.ParentID = &p.ID
		}
	}
	if v, ok := raw["due_date"]; ok {
		s := str(v)
		if s == "" {
			t.DueDate = nil
		} else {
			t.DueDate = task.DatePtr(&s)
		}
	}
	if err := task.Validate(t); err != nil {
		writeErr(w, 400, err.Error())
		return
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
		DueDate     *string `json:"due_date"`
		CreatedBy   *string `json:"created_by"`
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
		DueDate:     body.DueDate,
		CreatedBy:   body.CreatedBy,
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
	limit := 5
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 100 {
		limit = n
	}
	tasks, err := st.Recent(r.Context(), limit)
	if err != nil {
		writeStoreErr(w, 500, err)
		return
	}
	if tasks == nil {
		tasks = []task.Task{}
	}
	writeJSON(w, map[string]any{"tasks": tasks})
}

// handleNext claims the next ready task for a handle; the body is the task
// or null when none is ready.
func (s *Server) handleNext(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store(w, r)
	if !ok {
		return
	}
	var body struct {
		Handle string `json:"handle"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Handle == "" {
		writeErr(w, 400, "handle is required")
		return
	}
	t, err := st.Next(r.Context(), body.Handle)
	if err != nil {
		writeStoreErr(w, 500, err)
		return
	}
	writeJSON(w, t)
}

func (s *Server) handleFindExternalRef(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store(w, r)
	if !ok {
		return
	}
	t, err := st.FindByExternalRef(r.Context(), r.PathValue("source"), r.PathValue("external_id"))
	if err != nil {
		writeStoreErr(w, 404, err)
		return
	}
	writeJSON(w, t)
}

func (s *Server) handleUpsertExternalRef(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store(w, r)
	if !ok {
		return
	}
	var body struct {
		TaskID string  `json:"task_id"`
		URL    *string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TaskID == "" {
		writeErr(w, 400, "task_id is required")
		return
	}
	if err := st.UpsertExternalRef(r.Context(), body.TaskID, r.PathValue("source"), r.PathValue("external_id"), body.URL); err != nil {
		writeStoreErr(w, 500, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleEvents streams change events as server-sent events until the
// client disconnects or the store ends the subscription.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store(w, r)
	if !ok {
		return
	}
	sub, ok := st.(task.Subscriber)
	flusher, canFlush := w.(http.Flusher)
	if !ok || !canFlush {
		writeErr(w, http.StatusNotImplemented, "live updates are not available")
		return
	}
	events, err := sub.Subscribe(r.Context())
	if err != nil {
		writeStoreErr(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // don't let nginx buffer the stream
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "event: change\ndata: %s\n\n", data)
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		}
		flusher.Flush()
	}
}
