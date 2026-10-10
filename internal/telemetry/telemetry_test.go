package telemetry

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	want := `http_route="GET /api/v1/tasks/{id}"`
	if !strings.Contains(body, want) {
		t.Fatalf("metrics missing %s:\n%s", want, body)
	}
	if strings.Contains(body, "ab12") {
		t.Errorf("raw path leaked into metric labels")
	}
	for _, m := range []string{"http_server_request_total", "http_server_request_duration_seconds_bucket", "http_response_status_code=\"418\""} {
		if !strings.Contains(body, m) {
			t.Errorf("metrics missing %q", m)
		}
	}
}
