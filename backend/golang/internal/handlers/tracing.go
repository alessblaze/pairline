// Pairline - Open Source Video Chat and Matchmaking
// Copyright (C) 2026 Albert Blasczykowski
// Aless Microsystems
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

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
	ctx, span := handlerTracer.Start(c.Request.Context(), name, trace.WithSpanKind(trace.SpanKindServer))
	span.SetAttributes(
		attribute.String("pairline.span.layer", "server"),
		attribute.String("pairline.operation.name", name),
	)
	c.Request = c.Request.WithContext(ctx)
	return span
}

func startChildSpanFromContext(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	childCtx, span := handlerTracer.Start(ctx, name, trace.WithSpanKind(trace.SpanKindInternal))
	baseAttrs := []attribute.KeyValue{
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
