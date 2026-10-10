package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/jrdn/tt/internal/task"
)

// hub listens for task_events notifications on one dedicated Postgres
// connection and fans them out to live subscribers by project.
type hub struct {
	mu   sync.Mutex
	subs map[string]map[chan task.Event]struct{} // project id -> subscribers

	ready     chan struct{} // closed once the first LISTEN succeeds
	readyOnce sync.Once
}

func newHub() *hub {
	return &hub{subs: map[string]map[chan task.Event]struct{}{}, ready: make(chan struct{})}
}

// subscribe registers for a project's events. Call cancel to unregister.
func (h *hub) subscribe(projectID string) (ch chan task.Event, cancel func()) {
	ch = make(chan task.Event, 16)
	h.mu.Lock()
	if h.subs[projectID] == nil {
		h.subs[projectID] = map[chan task.Event]struct{}{}
	}
	h.subs[projectID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs[projectID], ch)
		h.mu.Unlock()
	}
}

func (h *hub) publish(projectID string, ev task.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[projectID] {
		send(ch, ev)
	}
}

// publishAll tells every subscriber that something may have changed, after
// a reconnect during which notifications could have been missed.
func (h *hub) publishAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, chans := range h.subs {
		for ch := range chans {
			send(ch, task.Event{})
		}
	}
}

func send(ch chan task.Event, ev task.Event) {
	select {
	case ch <- ev:
	default:
		// Subscriber is behind; it already has refreshes queued.
	}
}

// run listens until ctx ends, reconnecting with backoff.
func (h *hub) run(ctx context.Context, url string) {
	backoff := time.Second
	for ctx.Err() == nil {
		err := h.listen(ctx, url)
		if ctx.Err() != nil {
			return
		}
		slog.WarnContext(ctx, "live events disconnected", "err", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (h *hub) listen(ctx context.Context, url string) error {
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, "LISTEN tt_task_events"); err != nil {
		return err
	}
	select {
	case <-h.ready:
		h.publishAll() // reconnected: we may have missed events
	default:
		h.readyOnce.Do(func() { close(h.ready) })
	}
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		var msg struct {
			ProjectID string `json:"project_id"`
			TaskID    string `json:"task_id"`
			Action    string `json:"action"`
		}
		if err := json.Unmarshal([]byte(n.Payload), &msg); err != nil {
			slog.WarnContext(ctx, "live events: bad payload", "payload", n.Payload, "err", err)
			continue
		}
		_, span := tracer.Start(ctx, "live-events.dispatch", trace.WithAttributes(attribute.String("tt.action", msg.Action)))
		h.publish(msg.ProjectID, task.Event{TaskID: msg.TaskID, Action: msg.Action})
		span.End()
	}
}
