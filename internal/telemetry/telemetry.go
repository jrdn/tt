// Package telemetry wires OpenTelemetry metrics and logs for tt server.
//
// Metrics are recorded with the OTel SDK and exposed in Prometheus format by
// MetricsHandler, for Prometheus or Alloy to scrape. Logs go to stderr as
// JSON, where Alloy collects them from the container output.
package telemetry

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

const scope = "github.com/jrdn/tt"

// Telemetry holds the metric instruments and the exporters' lifecycle.
type Telemetry struct {
	metricsHandler http.Handler
	requests       metric.Int64Counter
	duration       metric.Float64Histogram
	inflight       metric.Int64UpDownCounter
	shutdown       []func(context.Context) error
}

// Setup configures metrics and installs a JSON slog default. The standard log package is routed through it, so existing
// log.Printf calls become structured records.
func Setup(ctx context.Context) (*Telemetry, error) {
	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithAttributes(semconv.ServiceName("tt-server")),
	)
	if err != nil {
		return nil, err
	}
	t := &Telemetry{}

	reg, err := otelprom.New()
	if err != nil {
		return nil, err
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reg), sdkmetric.WithResource(res))
	t.shutdown = append(t.shutdown, mp.Shutdown)
	otel.SetMeterProvider(mp)
	t.metricsHandler = promhttp.Handler()

	m := mp.Meter(scope)
	if t.requests, err = m.Int64Counter("http.server.request", metric.WithDescription("HTTP requests handled")); err != nil {
		return nil, err
	}
	if t.duration, err = m.Float64Histogram("http.server.request.duration", metric.WithUnit("s"),
		metric.WithDescription("HTTP request duration")); err != nil {
		return nil, err
	}
	if t.inflight, err = m.Int64UpDownCounter("http.server.active_requests", metric.WithDescription("In-flight HTTP requests")); err != nil {
		return nil, err
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	return t, nil
}

// MetricsHandler serves the Prometheus exposition format.
func (t *Telemetry) MetricsHandler() http.Handler { return t.metricsHandler }

// Shutdown flushes exporters.
func (t *Telemetry) Shutdown(ctx context.Context) {
	for _, f := range t.shutdown {
		f(ctx)
	}
}

// Middleware records request count, duration and in-flight requests, and logs
// each request. The route label is the mux pattern, which keeps cardinality
// bounded (never the raw path, which contains task ids and project slugs).
func (t *Telemetry) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		t.inflight.Add(ctx, 1)
		defer t.inflight.Add(ctx, -1)
		start := time.Now()
		rec := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		attrs := metric.WithAttributes(
			attribute.String("http.request.method", r.Method),
			attribute.String("http.route", route),
			attribute.String("http.response.status_code", strconv.Itoa(rec.status)),
		)
		elapsed := time.Since(start)
		t.requests.Add(ctx, 1, attrs)
		t.duration.Record(ctx, elapsed.Seconds(), attrs)
		slog.LogAttrs(ctx, levelFor(rec.status), "http request",
			slog.String("method", r.Method), slog.String("route", route),
			slog.Int("status", rec.status), slog.Duration("duration", elapsed))
	})
}

func levelFor(status int) slog.Level {
	if status >= 500 {
		return slog.LevelError
	}
	return slog.LevelInfo
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the underlying writer (the live
// events stream needs Flush).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
