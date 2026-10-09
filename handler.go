package apperror

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Reporter sends an unexpected error to an error tracker (e.g. Sentry, see the sentryreport package).
type Reporter func(ctx context.Context, err error)

// HandlerOption configures ErrorHandler.
type HandlerOption func(*ErrorHandler)

// WithLogger sets the function returning the logger for a request context.
// Defaults to slog.Default().
func WithLogger(fn func(ctx context.Context) *slog.Logger) HandlerOption {
	return func(h *ErrorHandler) { h.logger = fn }
}

// WithReporter sets the reporter for unexpected errors: internal *AppError and errors
// of unknown origin. Without it nothing is reported.
func WithReporter(fn Reporter) HandlerOption {
	return func(h *ErrorHandler) { h.report = fn }
}

// WithRequestAttr enables logging of the request for failed calls (the gRPC request message
// or *http.Request). The request is not logged by default because it may contain credentials
// and PII, so fn is responsible for redaction and should be cheap. Stream calls have no request.
func WithRequestAttr(fn func(req any) slog.Attr) HandlerOption {
	return func(h *ErrorHandler) { h.requestAttr = fn }
}

// ErrorHandler applies a single error policy to gRPC and HTTP servers:
//
//   - log level depends on the error: server-side failures are logged at Error,
//     timeouts at Warn, client errors and cancellations at Info;
//   - context cancellation and deadline errors (even wrapped into *AppError) become
//     codes.Canceled / codes.DeadlineExceeded and are not reported;
//   - *AppError and gRPC status errors are preserved;
//   - errors of unknown origin are wrapped into an internal *AppError;
//   - panics are recovered and turned into an internal *AppError with the stack
//     of the panic site (http.ErrAbortHandler is re-panicked, as net/http expects);
//   - internal *AppError, errors of unknown origin and panics are sent to the Reporter;
//   - the OpenTelemetry span in the context gets app.error.* attributes; server-side
//     failures (5xx) and panics are also recorded as an exception event with the stack
//     trace and set the span status to Error.
type ErrorHandler struct {
	systemCode  string
	logger      func(ctx context.Context) *slog.Logger
	report      Reporter
	requestAttr func(req any) slog.Attr
}

// NewErrorHandler creates an ErrorHandler; systemCode is used for wrapped unknown errors.
func NewErrorHandler(systemCode string, opts ...HandlerOption) *ErrorHandler {
	h := &ErrorHandler{
		systemCode: systemCode,
		logger:     func(context.Context) *slog.Logger { return slog.Default() },
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// handle logs and reports err and returns it normalized for a gRPC response.
// attrs are added to the log record.
func (h *ErrorHandler) handle(ctx context.Context, method string, req any, err error, attrs ...slog.Attr) error {
	const msg = "request failed"

	logger := h.logger(ctx).With(slog.String("method", method), slog.Any("error", err))
	if h.requestAttr != nil && req != nil {
		logger = logger.With(h.requestAttr(req))
	}
	for _, attr := range attrs {
		logger = logger.With(attr)
	}

	// Checked before *AppError: repositories often wrap a canceled DB query into an internal error.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		st := status.FromContextError(err)
		t := typeFromGRPCCode(st.Code())
		logger.Log(ctx, t.logLevel(), msg)
		recordSpan(ctx, t, err, nil)
		return st.Err()
	}

	if appErr, ok := errors.AsType[*AppError](err); ok {
		logger.Log(ctx, appErr.Type.logLevel(), msg)
		recordSpan(ctx, appErr.Type, err, appErr)
		if appErr.Type == TypeInternal {
			h.reportErr(ctx, err)
		}
		return err
	}

	if st, ok := status.FromError(err); ok && st.Code() != codes.Unknown {
		t := typeFromGRPCCode(st.Code())
		logger.Log(ctx, t.logLevel(), msg)
		recordSpan(ctx, t, err, nil)
		return err
	}

	logger.Error(msg)

	internalError := NewInternalError(
		h.systemCode,
		WithMessage("unknown internal system error"),
		WithErr(err),
	).WithTrace(ctx)

	recordSpan(ctx, TypeInternal, internalError, internalError)
	h.reportErr(ctx, internalError)

	return internalError
}

func (h *ErrorHandler) reportErr(ctx context.Context, err error) {
	if h.report != nil {
		h.report(ctx, err)
	}
}

// logLevel returns the log level for a failed request of this type.
func (t Type) logLevel() slog.Level {
	switch {
	case t == TypeTimeout:
		return slog.LevelWarn
	case t.HTTPStatus() >= 500:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
