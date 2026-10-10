package server

import "go.opentelemetry.io/otel"

var tracer = otel.Tracer("github.com/jrdn/tt/internal/server")
