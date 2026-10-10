// Package telemetry wires OpenTelemetry metrics and logs for tt server.
//
// Metrics are recorded with the OTel SDK and exposed in Prometheus format by
// MetricsHandler, for Prometheus or Alloy to scrape. Logs go to stderr as
// JSON, where Alloy collects them from the container output. Traces are pushed
// over OTLP/HTTP when OTEL_EXPORTER_OTLP_ENDPOINT (or the _TRACES_ variant) is
// set, e.g. to Alloy's otelcol.receiver.otlp; without it spans are still
// created, so log lines carry trace ids, but are not exported.
package telemetry

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const scope = "github.com/jrdn/tt"

// Telemetry holds the metric instruments and the exporters' lifecycle.
type Telemetry struct {
	metricsHandler http.Handler
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

	topts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != "" {
		exp, err := otlptracehttp.New(ctx)
		if err != nil {
			return nil, err
		}
		topts = append(topts, sdktrace.WithBatcher(exp))
	}
	tp := sdktrace.NewTracerProvider(topts...)
	t.shutdown = append(t.shutdown, tp.Shutdown)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

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

// Middleware traces each request and records the standard HTTP server metrics
// (request duration histogram and active requests, labelled with the mux route
// pattern rather than the raw path, which contains task ids and project slugs).
// It also logs one line per request, carrying the trace id.
func (t *Telemetry) Middleware(next http.Handler) http.Handler {
	logged := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		// The span starts before routing; name it by pattern once known.
		trace.SpanFromContext(r.Context()).SetName(r.Method + " " + route)
		attrs := []slog.Attr{
			slog.String("method", r.Method), slog.String("route", route),
			slog.Int("status", rec.status), slog.Duration("duration", time.Since(start)),
		}
		if sc := trace.SpanContextFromContext(r.Context()); sc.IsValid() {
			attrs = append(attrs, slog.String("trace_id", sc.TraceID().String()))
		}
		slog.LogAttrs(r.Context(), levelFor(rec.status), "http request", attrs...)
	})
	return otelhttp.NewHandler(logged, "http.server", otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
		return r.Method
	}))
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
