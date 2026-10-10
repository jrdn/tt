package telemetry

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestMiddlewareRecordsRouteMetrics(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	tel, err := Setup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tel.Shutdown(context.Background()) })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks/{id}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := tel.Middleware(mux)
	for _, id := range []string{"ab12", "cd34"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/tasks/"+id, nil))
	}

	rec := httptest.NewRecorder()
	tel.MetricsHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	// The route is the pattern, so both ids land in one series.
	want := `http_route="/api/v1/tasks/{id}"`
	if !strings.Contains(body, want) {
		t.Fatalf("metrics missing %s:\n%s", want, body)
	}
	if strings.Contains(body, "ab12") {
		t.Errorf("raw path leaked into metric labels")
	}
	if strings.Contains(body, "server_address") {
		t.Errorf("client-controlled server_address leaked into metric labels")
	}
	for _, m := range []string{"http_server_request_duration_seconds_bucket", "http_response_status_code=\"418\""} {
		if !strings.Contains(body, m) {
			t.Errorf("metrics missing %q", m)
		}
	}
}

func TestTraceHandlerAddsTraceID(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(traceHandler{slog.NewJSONHandler(&buf, nil)})
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled,
	})
	log.InfoContext(trace.ContextWithSpanContext(context.Background(), sc), "with span")
	log.Info("without span")
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if !strings.Contains(lines[0], `"trace_id":"`+sc.TraceID().String()+`"`) {
		t.Errorf("line with span = %s, want trace_id", lines[0])
	}
	if strings.Contains(lines[1], "trace_id") {
		t.Errorf("line without span = %s, want no trace_id", lines[1])
	}
}
