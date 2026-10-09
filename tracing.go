package apperror

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
)

// Span attributes describing a failed request.
const (
	attrErrorType       = attribute.Key("app.error.type")
	attrErrorSystemCode = attribute.Key("app.error.system_code")
	attrErrorCode       = attribute.Key("app.error.code")
)

// recordSpan records a failed request of type t on the span in ctx, if it is recording.
//
// Following OpenTelemetry semantic conventions, only server-side failures (5xx: internal,
// unavailable, timeout, ...) are recorded as an exception event and mark the span as failed;
// client errors and cancellations only add attributes. appErr may be nil.
func recordSpan(ctx context.Context, t Type, err error, appErr *AppError) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	attrs := []attribute.KeyValue{attrErrorType.String(t.String())}
	if appErr != nil {
		attrs = append(attrs,
			attrErrorSystemCode.String(appErr.SystemCode),
			attrErrorCode.Int64(int64(appErr.Code)),
		)
	}
	span.SetAttributes(attrs...)

	if t.HTTPStatus() < 500 {
		return
	}

	var eventAttrs []attribute.KeyValue
	if appErr != nil && len(appErr.stack) > 0 {
		eventAttrs = append(eventAttrs, semconv.ExceptionStacktrace(formatStack(appErr.stack)))
	}
	span.RecordError(err, trace.WithAttributes(eventAttrs...))
	span.SetStatus(otelcodes.Error, err.Error())
}
