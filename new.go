package apperror

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/trace"
)

// NewInternalError creates a new internal system error.
func NewInternalError(systemCode string, options ...Option) *AppError {
	return newAppError(TypeInternal, systemCode, options...)
}

// NewBadRequestError creates a new bad request error.
func NewBadRequestError(systemCode string, options ...Option) *AppError {
	return newAppError(TypeBadRequest, systemCode, options...)
}

// NewValidationError creates a new validation error.
func NewValidationError(systemCode string, options ...Option) *AppError {
	return newAppError(TypeValidation, systemCode, options...)
}

// NewNotFoundError creates a new not-found error.
func NewNotFoundError(systemCode string, options ...Option) *AppError {
	return newAppError(TypeNotFound, systemCode, options...)
}

// NewUnauthorizedError creates a new unauthorized error.
func NewUnauthorizedError(systemCode string, options ...Option) *AppError {
	return newAppError(TypeUnauthorized, systemCode, options...)
}

// NewForbiddenError creates a new forbidden error.
func NewForbiddenError(systemCode string, options ...Option) *AppError {
	return newAppError(TypeForbidden, systemCode, options...)
}

// NewConditionFailedError creates a new precondition-failed error.
func NewConditionFailedError(systemCode string, options ...Option) *AppError {
	return newAppError(TypeConditionFailed, systemCode, options...)
}

// NewConflictError creates a new conflict error (resource already exists or concurrent modification).
func NewConflictError(systemCode string, options ...Option) *AppError {
	return newAppError(TypeConflict, systemCode, options...)
}

// NewTooManyRequestsError creates a new rate-limit / quota exhausted error.
func NewTooManyRequestsError(systemCode string, options ...Option) *AppError {
	return newAppError(TypeTooManyRequests, systemCode, options...)
}

// NewUnavailableError creates a new service-unavailable error (safe for clients to retry).
func NewUnavailableError(systemCode string, options ...Option) *AppError {
	return newAppError(TypeUnavailable, systemCode, options...)
}

// NewTimeoutError creates a new timeout error.
func NewTimeoutError(systemCode string, options ...Option) *AppError {
	return newAppError(TypeTimeout, systemCode, options...)
}

// NewCanceledError creates a new canceled error.
func NewCanceledError(systemCode string, options ...Option) *AppError {
	return newAppError(TypeCanceled, systemCode, options...)
}

// NewNotImplementedError creates a new not-implemented error.
func NewNotImplementedError(systemCode string, options ...Option) *AppError {
	return newAppError(TypeNotImplemented, systemCode, options...)
}

// Option is a functional option for configuring an *AppError.
type Option func(*AppError)

// WithErr wraps an underlying error.
func WithErr(err error) Option {
	return func(ae *AppError) { ae.Err = err }
}

// WithMessage sets the human-readable error message exposed to clients.
// Without it a safe default message for the error type is used; the wrapped error text is never exposed.
func WithMessage(message string) Option {
	return func(ae *AppError) { ae.Message = message }
}

// WithDomain sets the error domain.
func WithDomain(domain string) Option {
	return func(ae *AppError) { ae.Domain = domain }
}

// WithCode sets the application-specific numeric error code.
func WithCode(code uint32) Option {
	return func(ae *AppError) { ae.Code = code }
}

// WithFields appends field violations without a reason, sorted by field name.
func WithFields(fields ErrorFields) Option {
	return func(ae *AppError) { ae.Violations = append(ae.Violations, fields.violations()...) }
}

// WithViolations appends field violations in the given order.
func WithViolations(violations ...FieldViolation) Option {
	return func(ae *AppError) { ae.Violations = append(ae.Violations, violations...) }
}

// WithRetryAfter tells clients when to retry; typically used with
// NewTooManyRequestsError and NewUnavailableError.
func WithRetryAfter(d time.Duration) Option {
	return func(ae *AppError) { ae.RetryAfter = d }
}

// WithTraceID manually sets a trace ID.
func WithTraceID(traceID string) Option {
	return func(ae *AppError) { ae.TraceID = traceID }
}

// WithTrace extracts the OpenTelemetry trace ID from context during construction.
func WithTrace(ctx context.Context) Option {
	return func(ae *AppError) {
		if span := trace.SpanContextFromContext(ctx); span.HasTraceID() {
			ae.TraceID = span.TraceID().String()
		}
	}
}
