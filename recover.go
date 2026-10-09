package apperror

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"slices"
	"strings"

	goerrors "github.com/jwm1rr0rb10/go-errors"
)

// recoverGRPC converts a panic in a gRPC handler into an internal error stored in *err.
// It must be deferred directly so that recover works.
func (h *ErrorHandler) recoverGRPC(ctx context.Context, method string, req any, err *error) {
	rec := recover()
	if rec == nil {
		return
	}
	panicErr, attrs := h.panicError(ctx, rec)
	*err = h.handle(ctx, method, req, panicErr, attrs...)
}

// recoverHTTP writes a panic in an HTTP handler as an internal error.
// It must be deferred directly so that recover works.
func (h *ErrorHandler) recoverHTTP(w http.ResponseWriter, r *http.Request) {
	rec := recover()
	if rec == nil {
		return
	}
	// net/http convention: ErrAbortHandler aborts the response silently.
	if rec == http.ErrAbortHandler {
		panic(rec)
	}
	panicErr, attrs := h.panicError(r.Context(), rec)
	h.writeHTTPError(w, r, panicErr, attrs...)
}

// HTTPMiddleware recovers panics in next and writes them as internal errors.
// Use it for plain http.Handler; handlers created by HTTP already recover panics.
func (h *ErrorHandler) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer h.recoverHTTP(w, r)
		next.ServeHTTP(w, r)
	})
}

// panicError converts a recovered panic value into an internal *AppError whose stack trace
// starts at the panic site, plus log attributes with the panic value and the stack.
// It must be called from the deferred function that recovered the panic.
func (h *ErrorHandler) panicError(ctx context.Context, rec any) (*AppError, []slog.Attr) {
	cause, ok := rec.(error)
	if !ok {
		cause = errors.New(fmt.Sprint(rec))
	}

	panicErr := NewInternalError(h.systemCode, WithErr(goerrors.Wrap(cause, "panic")), WithTrace(ctx))
	panicErr.stack = panicStack()

	return panicErr, []slog.Attr{
		slog.String("panic", fmt.Sprint(rec)),
		slog.String("stack", formatStack(panicErr.stack)),
	}
}

// panicStack returns the stack of the panicking goroutine starting at the panic site.
func panicStack() []uintptr {
	const depth = 64
	var pcs [depth]uintptr
	stack := pcs[:runtime.Callers(3, pcs[:])]

	for i, pc := range stack {
		if funcName(pc) != "runtime.gopanic" {
			continue
		}
		stack = stack[i+1:]
		// Drop runtime frames between gopanic and the panic site (e.g. runtime.sigpanic for nil dereference).
		for len(stack) > 0 && strings.HasPrefix(funcName(stack[0]), "runtime.") {
			stack = stack[1:]
		}
		break
	}
	return slices.Clone(stack)
}

func funcName(pc uintptr) string {
	if fn := runtime.FuncForPC(pc - 1); fn != nil {
		return fn.Name()
	}
	return ""
}

func formatStack(pcs []uintptr) string {
	if len(pcs) == 0 {
		return ""
	}
	var b strings.Builder
	frames := runtime.CallersFrames(pcs)
	for {
		frame, more := frames.Next()
		fmt.Fprintf(&b, "%s\n\t%s:%d\n", frame.Function, frame.File, frame.Line)
		if !more {
			break
		}
	}
	return b.String()
}
