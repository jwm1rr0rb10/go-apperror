// Package apperror provides a standardized, structured error type (*AppError) for Go microservices.
//
// Features:
//   - Typed errors mapped to gRPC and HTTP status codes
//   - Rich gRPC details (ErrorInfo with "SYSTEMCODE-CODE" reason + BadRequest field violations)
//   - OpenTelemetry trace ID attachment
//   - JSON marshaling for HTTP responses
//   - gRPC unary/stream interceptors and an HTTP handler adapter with logging and
//     pluggable reporting of unexpected errors (see ErrorHandler)
//   - Client-side helper to map gRPC ErrorInfo.Reason back to domain errors
//   - Full support for errors.Is / errors.As / errors.Unwrap
//   - Stack traces for internal errors, compatible with Sentry
//
// Usage example (server):
//
//	err := NewNotFoundError("PS", WithCode(404), WithMessage("user not found"), WithDomain("users"))
//	err = err.WithTrace(ctx).WithFields(ErrorFields{"user_id": "123"})
//
// Usage example (client):
//
//	st, _ := status.FromError(grpcErr)
//	if mapped := ErrorInfoFromDetails(st, reasonHandlers); mapped != nil { ... }
package apperror

import (
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	goerrors "github.com/jwm1rr0rb10/go-errors"
	"go.opentelemetry.io/otel/trace"
)

// ErrorFields is a shorthand for field violations without a reason: field name -> description.
type ErrorFields map[string]string

// FieldViolation describes a single invalid field. A field may have several violations.
type FieldViolation struct {
	Field string `json:"field"`
	// Reason is a stable machine-readable code (e.g. "REQUIRED", "TOO_LONG")
	// that clients can use for localization.
	Reason      string `json:"reason,omitempty"`
	Description string `json:"description"`
}

// violations converts fields into violations sorted by field name.
func (f ErrorFields) violations() []FieldViolation {
	out := make([]FieldViolation, 0, len(f))
	for _, field := range slices.Sorted(maps.Keys(f)) {
		out = append(out, FieldViolation{Field: field, Description: f[field]})
	}
	return out
}

// AppError is the central structured error type.
type AppError struct {
	Err        error            `json:"-"`
	Message    string           `json:"message"`
	Domain     string           `json:"domain,omitempty"`
	SystemCode string           `json:"system_code,omitempty"`
	Type       Type             `json:"type"`
	Code       uint32           `json:"code"`
	Violations []FieldViolation `json:"violations,omitempty"`
	TraceID    string           `json:"trace_id,omitempty"`
	// RetryAfter tells clients when to retry (gRPC RetryInfo, HTTP Retry-After header).
	RetryAfter time.Duration `json:"-"`

	stack []uintptr
}

// clone returns a copy safe for chaining without mutating the original.
func (e *AppError) clone() *AppError {
	if e == nil {
		return nil
	}

	copied := *e
	copied.Violations = slices.Clone(e.Violations)
	return &copied
}

// WithFields returns a copy with field violations appended (existing ones are kept).
func (e *AppError) WithFields(fields ErrorFields) *AppError {
	return e.WithViolations(fields.violations()...)
}

// WithViolations returns a copy with field violations appended (existing ones are kept).
func (e *AppError) WithViolations(violations ...FieldViolation) *AppError {
	if e == nil {
		return nil
	}

	copied := e.clone()
	copied.Violations = append(copied.Violations, violations...)
	return copied
}

// newAppError is the internal constructor.
func newAppError(errType Type, systemCode string, options ...Option) *AppError {
	if systemCode == "" {
		systemCode = "UNKNOWN" // safety validation
	}

	ae := &AppError{
		Type:       errType,
		SystemCode: systemCode,
	}

	for _, opt := range options {
		opt(ae)
	}

	// Message is exposed to clients, so it is never derived from the wrapped error:
	// its text may contain internal details (SQL, hosts, credentials).
	if ae.Message == "" {
		ae.Message = errType.defaultMessage()
	}

	// Stack is captured only for internal errors: they are the ones investigated in Sentry,
	// while client errors can be frequent and should stay cheap.
	if errType == TypeInternal {
		ae.stack = callers()
	}

	return ae
}

// Error returns "<message>: <cause> (code: <system_code>-<code>)" for logs; use Message for client-facing text.
// Fields and trace ID are deliberately omitted: high-cardinality values in the error string
// break grouping in Sentry and log aggregation. They are available via LogValue.
func (e *AppError) Error() string {
	var b strings.Builder
	b.WriteString(e.Message)

	if cause := e.cause(); cause != "" {
		b.WriteString(": ")
		b.WriteString(cause)
	}

	if e.Code != 0 || e.SystemCode != "" {
		b.WriteString(" (code: ")
		b.WriteString(e.SystemCode)
		b.WriteByte('-')
		b.WriteString(strconv.FormatUint(uint64(e.Code), 10))
		b.WriteByte(')')
	}

	return b.String()
}

// StackTrace returns program counters captured when an internal error was created, or nil.
// The method name and signature match what Sentry and similar tools look for.
func (e *AppError) StackTrace() []uintptr {
	return e.stack
}

func callers() []uintptr {
	const depth = 32
	var pcs [depth]uintptr
	// Skip runtime.Callers, callers, newAppError and the public constructor.
	n := runtime.Callers(4, pcs[:])
	return slices.Clone(pcs[:n])
}

// WithTrace returns a copy with the OpenTelemetry trace ID attached.
func (e *AppError) WithTrace(ctx context.Context) *AppError {
	if e == nil {
		return nil
	}

	copied := e.clone()
	if span := trace.SpanContextFromContext(ctx); span.HasTraceID() {
		copied.TraceID = span.TraceID().String()
	}
	return copied
}

// cause returns the wrapped error on one line (multi-errors included) if it adds
// information beyond Message.
func (e *AppError) cause() string {
	if e.Err == nil {
		return ""
	}
	if text := goerrors.OneLine(e.Err); text != e.Message {
		return text
	}
	return ""
}

// LogValue implements slog.LogValuer. Without it JSON handlers would log the
// client-facing MarshalJSON output and drop the wrapped cause.
func (e *AppError) LogValue() slog.Value {
	attrs := []slog.Attr{
		slog.String("message", e.Message),
		slog.String("type", e.Type.String()),
		slog.String("system_code", e.SystemCode),
		slog.Uint64("code", uint64(e.Code)),
	}
	if cause := e.cause(); cause != "" {
		attrs = append(attrs, slog.String("cause", cause))
	}
	if e.Domain != "" {
		attrs = append(attrs, slog.String("domain", e.Domain))
	}
	if len(e.Violations) > 0 {
		attrs = append(attrs, slog.Any("violations", e.Violations))
	}
	if e.TraceID != "" {
		attrs = append(attrs, slog.String("trace_id", e.TraceID))
	}
	if e.RetryAfter > 0 {
		attrs = append(attrs, slog.Duration("retry_after", e.RetryAfter))
	}
	return slog.GroupValue(attrs...)
}

// Unwrap supports errors.Unwrap.
func (e *AppError) Unwrap() error { return e.Err }

// Is implements errors.Is support. Errors match by type, system code and code;
// errors without a code (0) are distinguishable only by message, so it is compared too.
func (e *AppError) Is(target error) bool {
	other, ok := target.(*AppError)
	if !ok || other == nil {
		return false
	}
	if e.Type != other.Type || e.SystemCode != other.SystemCode || e.Code != other.Code {
		return false
	}
	return e.Code != 0 || e.Message == other.Message
}

// MarshalJSON implements json.Marshaler.
func (e *AppError) MarshalJSON() ([]byte, error) {
	type alias AppError
	return json.Marshal((*alias)(e))
}

// Marshal returns JSON representation (without wrapped Err).
func (e *AppError) Marshal() []byte {
	bytes, err := e.MarshalJSON()
	if err != nil {
		return nil
	}
	return bytes
}
