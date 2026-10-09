package apperror

import (
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"

	"google.golang.org/grpc/status"
)

// httperror is the interface used by FromError.
type httperror interface {
	error
	HTTPError() *AppError
}

// FromError extracts *AppError from any error (direct or wrapped).
func FromError(err error) (*AppError, bool) {
	if err == nil {
		return nil, false
	}

	if gs, ok := errors.AsType[httperror](err); ok {
		if httpErr := gs.HTTPError(); httpErr != nil {
			return httpErr, true
		}
	}

	return nil, false
}

// HTTPError returns a copy of the AppError (implements httperror interface).
// A copy is returned so that callers can't mutate shared sentinel errors.
func (e *AppError) HTTPError() *AppError {
	return e.clone()
}

// HTTPStatus returns the HTTP status code for this error.
func (e *AppError) HTTPStatus() int {
	return e.Type.HTTPStatus()
}

// HandlerFunc is an HTTP handler that returns an error instead of writing it.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// HTTP adapts fn into an http.Handler; a returned error is written with WriteHTTPError
// and a panic is recovered (see HTTPMiddleware).
// fn must not write the response before returning an error.
func (h *ErrorHandler) HTTP(fn HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer h.recoverHTTP(w, r)

		if err := fn(w, r); err != nil {
			h.WriteHTTPError(w, r, err)
		}
	})
}

// WriteHTTPError logs and reports err with the same policy as the gRPC interceptors
// and writes it as a JSON response with the matching HTTP status.
func (h *ErrorHandler) WriteHTTPError(w http.ResponseWriter, r *http.Request, err error) {
	h.writeHTTPError(w, r, err)
}

func (h *ErrorHandler) writeHTTPError(w http.ResponseWriter, r *http.Request, err error, attrs ...slog.Attr) {
	method := r.Pattern
	if method == "" {
		method = r.Method + " " + r.URL.Path
	}

	appErr := h.toAppError(h.handle(r.Context(), method, r, err, attrs...))

	w.Header().Set("Content-Type", "application/json")
	if appErr.RetryAfter > 0 {
		// Retry-After is in whole seconds; round up so clients never retry too early.
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(appErr.RetryAfter.Seconds()))))
	}
	w.WriteHeader(appErr.HTTPStatus())
	_, _ = w.Write(appErr.Marshal())
}

// toAppError converts an error normalized by handle into *AppError.
func (h *ErrorHandler) toAppError(err error) *AppError {
	if appErr, ok := errors.AsType[*AppError](err); ok {
		return appErr
	}

	st, _ := status.FromError(err)
	if restored, ok := FromGRPCStatus(st); ok {
		return restored
	}
	// A plain status message may come from another service, so it is not exposed.
	return newAppError(typeFromGRPCCode(st.Code()), h.systemCode)
}
