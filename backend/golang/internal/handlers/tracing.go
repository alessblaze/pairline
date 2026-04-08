package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"runtime"
	"strings"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var handlerTracer = otel.Tracer("pairline/go/handlers")

func startHandlerSpan(c *gin.Context, name string) trace.Span {
	ctx, span := handlerTracer.Start(c.Request.Context(), name, trace.WithSpanKind(trace.SpanKindInternal))
	span.SetAttributes(
		attribute.String("span.kind", "internal"),
		attribute.String("pairline.span.layer", "internal"),
		attribute.String("pairline.operation.name", name),
	)
	c.Request = c.Request.WithContext(ctx)
	return span
}

func startChildSpanFromContext(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	childCtx, span := handlerTracer.Start(ctx, name, trace.WithSpanKind(trace.SpanKindInternal))
	baseAttrs := []attribute.KeyValue{
		attribute.String("span.kind", "internal"),
		attribute.String("pairline.span.layer", "internal"),
		attribute.String("pairline.operation.name", name),
	}
	if pc, _, _, ok := runtime.Caller(1); ok {
		if fn := runtime.FuncForPC(pc); fn != nil {
			baseAttrs = append(baseAttrs, attribute.String("code.function", fn.Name()))
		}
	}
	span.SetAttributes(append(baseAttrs, attrs...)...)
	return childCtx, span
}

func hashedAttribute(key, value string) attribute.KeyValue {
	return attribute.String(key, stableRef(value))
}

func stableRef(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:6])
}
