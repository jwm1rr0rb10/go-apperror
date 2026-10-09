# apperror

Structured errors for Go services with gRPC and HTTP APIs.

One `*AppError` type carries the error type, a client-facing message, an application code, field
violations, a retry hint and a trace ID. It converts to a gRPC status (with `ErrorInfo`, `BadRequest`
and `RetryInfo` details) and to an HTTP JSON response. `ErrorHandler` applies one policy to gRPC and
HTTP servers: logging, error reporting, panic recovery and OpenTelemetry span recording.

---

## Features

- 13 error types mapped to gRPC codes and HTTP statuses (see [Error types](#error-types))
- gRPC: `GRPCStatus()` with `ErrorInfo` (`SYSTEMCODE-CODE` reason), `BadRequest` field violations and
  `RetryInfo`; `FromGRPCStatus()` / `FromGRPCError()` restore `*AppError` on the client
- HTTP: JSON body with the type serialized by name, `Retry-After` header
- The client-facing `Message` is never taken from the wrapped error, so internal details don't leak
- `errors.Is` / `errors.As` / `errors.Unwrap`, and `IsNotFound(err)`-style helpers that also understand
  errors returned by gRPC clients
- `ErrorHandler` for gRPC (unary and stream interceptors) and HTTP (handler adapter and middleware):
  log level by error type, context-cancellation handling, panic recovery, OpenTelemetry span recording,
  pluggable error reporting
- Stack traces for internal errors and panics, picked up by Sentry and OpenTelemetry
- `slog.LogValuer` implementation for structured logs
- Dependencies: gRPC, genproto, OpenTelemetry, [go-errors](https://github.com/jwm1rr0rb10/go-errors)
  (`OneLine`, `Wrap`). Sentry is needed only by the optional `sentryreport` package

---

## Installation

```bash
go get -u github.com/jwm1rr0rb10/libraries/backend/golang/apperror
```

---

## Quick Start

```go
package main

import (
	"context"
	"log"
	"net/http"

	"github.com/jwm1rr0rb10/libraries/backend/golang/apperror"
	"github.com/jwm1rr0rb10/libraries/backend/golang/apperror/sentryreport"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"google.golang.org/grpc"
)

var ErrUserNotFound = apperror.NewNotFoundError("USERS",
	apperror.WithCode(1),
	apperror.WithMessage("user not found"),
)

func findUser(ctx context.Context, id string) error {
	return ErrUserNotFound.WithTrace(ctx)
}

func main() {
	h := apperror.NewErrorHandler("USERS", apperror.WithReporter(sentryreport.Report))

	// gRPC: handlers just return *AppError, it implements GRPCStatus().
	// The OTel stats handler creates the span before the interceptors run.
	grpcServer := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(h.UnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(h.StreamServerInterceptor()),
	)
	_ = grpcServer

	// HTTP: handlers return an error, the adapter writes it as JSON.
	mux := http.NewServeMux()
	mux.Handle("GET /users/{id}", h.HTTP(func(w http.ResponseWriter, r *http.Request) error {
		if err := findUser(r.Context(), r.PathValue("id")); err != nil {
			return err // 404 {"message":"user not found","system_code":"USERS","type":"NotFound","code":1}
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}))

	// otelhttp goes outside, so the span exists when the error is handled.
	log.Fatal(http.ListenAndServe(":8080", otelhttp.NewHandler(mux, "users")))
}
```

The HTTP part is checked by `example_test.go`.

---

## Creating errors

```go
err := apperror.NewNotFoundError("USERS",
	apperror.WithCode(1),
	apperror.WithMessage("user not found"),
	apperror.WithDomain("users"),
	apperror.WithTrace(ctx),
)
```

| Option | Purpose |
|---|---|
| `WithErr(error)` | Wrap the underlying cause (visible in logs and traces, never sent to clients) |
| `WithMessage(string)` | Client-facing message (default: safe message for the type) |
| `WithDomain(string)` | Business domain |
| `WithCode(uint32)` | Application-specific numeric code |
| `WithFields(ErrorFields)` | Append field violations from a `field -> description` map, sorted by field |
| `WithViolations(...FieldViolation)` | Append field violations with reason codes, in order |
| `WithRetryAfter(time.Duration)` | Tell clients when to retry (gRPC `RetryInfo`, HTTP `Retry-After`) |
| `WithTrace(context.Context)` | Attach the OpenTelemetry trace ID |
| `WithTraceID(string)` | Attach a trace ID manually |

Methods `WithTrace`, `WithFields` and `WithViolations` return a copy, so package-level sentinel errors
can be enriched safely.

### Sentinel errors and `errors.Is`

`errors.Is` compares type, system code and code. Errors without a code (0) are distinguishable only
by message, so it is compared too. Give sentinel errors a code to keep matching independent of the text:

```go
var ErrUserNotFound = apperror.NewNotFoundError("USERS", apperror.WithCode(1), apperror.WithMessage("user not found"))

if errors.Is(err, ErrUserNotFound) { ... } // also matches ErrUserNotFound.WithTrace(ctx)
```

### Error types

| Constructor | `Type` | HTTP | gRPC |
|---|---|---|---|
| `NewInternalError` | `TypeInternal` | 500 | `Internal` |
| `NewBadRequestError` | `TypeBadRequest` | 400 | `InvalidArgument` |
| `NewValidationError` | `TypeValidation` | 400 | `InvalidArgument` |
| `NewNotFoundError` | `TypeNotFound` | 404 | `NotFound` |
| `NewUnauthorizedError` | `TypeUnauthorized` | 401 | `Unauthenticated` |
| `NewForbiddenError` | `TypeForbidden` | 403 | `PermissionDenied` |
| `NewConditionFailedError` | `TypeConditionFailed` | 412 | `FailedPrecondition` |
| `NewConflictError` | `TypeConflict` | 409 | `AlreadyExists` |
| `NewTooManyRequestsError` | `TypeTooManyRequests` | 429 | `ResourceExhausted` |
| `NewUnavailableError` | `TypeUnavailable` | 503 | `Unavailable` |
| `NewTimeoutError` | `TypeTimeout` | 504 | `DeadlineExceeded` |
| `NewCanceledError` | `TypeCanceled` | 499 | `Canceled` |
| `NewNotImplementedError` | `TypeNotImplemented` | 501 | `Unimplemented` |

In JSON the type is serialized by name: `"type": "NotFound"`.

### Checking the type

```go
if apperror.IsNotFound(err) { ... }            // works through fmt.Errorf("%w") wrapping
if apperror.IsType(err, apperror.TypeConflict) { ... }
if t, ok := apperror.TypeOf(err); ok { ... }
```

The helpers also work for errors returned by gRPC clients: the type is restored from `ErrorInfo` or,
for a plain status, derived from the gRPC code.

### Message vs cause

`Message` is what clients see (gRPC status message, JSON `message`). It is **never** derived from the
wrapped error, because driver errors may contain SQL, hosts or credentials. Without `WithMessage`
a safe default for the type is used (`"internal error"`, `"not found"`, ...).

The cause stays available for logs and traces:

```go
err := apperror.NewInternalError("USERS", apperror.WithCode(2), apperror.WithErr(dbErr))
err.Message // "internal error"
err.Error() // "internal error: pq: connection refused (code: USERS-2)"
```

A multi-error cause (e.g. `errors.Join`) is rendered on one line: `"internal error: timeout; dial: refused"`.

`Error()` deliberately omits field violations and the trace ID: high-cardinality values in the error
string break grouping in Sentry and log aggregation. `*AppError` implements `slog.LogValuer`, so
structured loggers get `message`, `type`, `system_code`, `code`, `cause`, `domain`, `violations`,
`trace_id` and `retry_after`.

### Field violations

```go
err := apperror.NewValidationError("USERS",
	apperror.WithFields(apperror.ErrorFields{"email": "required"}),
	apperror.WithViolations(apperror.FieldViolation{
		Field:       "password",
		Reason:      "TOO_SHORT", // stable code, clients can localize by it
		Description: "min 8 chars",
	}),
)
```

```json
{"message":"validation failed","system_code":"USERS","type":"Validation","code":0,
 "violations":[{"field":"email","description":"required"},
               {"field":"password","reason":"TOO_SHORT","description":"min 8 chars"}]}
```

A field may have several violations. In gRPC they are sent as `BadRequest.FieldViolations`.

### Retry hints

```go
err := apperror.NewTooManyRequestsError("USERS", apperror.WithRetryAfter(1500*time.Millisecond))
```

The delay is sent as gRPC `RetryInfo` and as the HTTP `Retry-After` header (rounded up to whole
seconds: `2`). It is restored by `FromGRPCStatus`, so a delay from a downstream service reaches HTTP clients.

### Stack traces

Internal errors (`NewInternalError`) capture the stack of the constructor's caller; recovered panics
capture the stack of the panic site. The stack is exposed via `StackTrace() []uintptr`, which Sentry
recognizes, and is added to the OpenTelemetry exception event. Other types don't capture a stack, so
frequent client errors stay cheap.

---

## ErrorHandler

`ErrorHandler` applies one policy to gRPC and HTTP servers:

| Returned error | Response | Log level | Reported | Span status |
|---|---|---|---|---|
| `context.Canceled` (also wrapped, e.g. in an internal `*AppError`) | `Canceled` (HTTP 499) | Info | no | unchanged |
| `context.DeadlineExceeded` (also wrapped) | `DeadlineExceeded` (HTTP 504) | Warn | no | `Error` |
| `*AppError` of a client type (4xx) | as is | Info | no | unchanged |
| `*AppError` `TypeTimeout` | as is | Warn | no | `Error` |
| `*AppError` `TypeInternal` | as is | Error | yes | `Error` |
| other server types (`Unavailable`, `NotImplemented`) | as is | Error | no | `Error` |
| gRPC status error (e.g. from a downstream call) | as is | by code | no | by code |
| any other error | internal `*AppError` "unknown internal system error" | Error | yes | `Error` |
| panic | internal `*AppError` "internal error" | Error, with `panic` and `stack` | yes | `Error` |

Every log record contains `method` (gRPC full method or HTTP route pattern) and `error`.
"Reported" means sent to the `Reporter`, if one is configured.

```go
h := apperror.NewErrorHandler("USERS",
	apperror.WithLogger(logging.L),                // func(ctx) *slog.Logger; default: slog.Default()
	apperror.WithReporter(sentryreport.Report),    // default: nothing is reported
	apperror.WithRequestAttr(func(req any) slog.Attr {
		return slog.String("request", redact(req)) // default: request is not logged (PII)
	}),
)
```

Shorthands without a shared handler: `apperror.GRPCUnaryInterceptor("USERS", opts...)`,
`apperror.GRPCStreamInterceptor("USERS", opts...)`.

### gRPC

```go
grpc.NewServer(
	grpc.StatsHandler(otelgrpc.NewServerHandler()),
	grpc.ChainUnaryInterceptor(h.UnaryServerInterceptor()),
	grpc.ChainStreamInterceptor(h.StreamServerInterceptor()),
)
```

### HTTP

- `h.HTTP(fn)` adapts `func(w, r) error` into `http.Handler` and recovers panics.
  `fn` must not write the response before returning an error.
- `h.HTTPMiddleware(next)` recovers panics in a plain `http.Handler`, e.g. a whole router.
- `h.WriteHTTPError(w, r, err)` writes an error from an existing handler.
- An error returned by a downstream gRPC call is converted back: a status with `ErrorInfo` is restored
  via `FromGRPCStatus`, a plain status gets the type by code and a safe default message.

### Panics

A recovered panic becomes an internal `*AppError`: clients get `"internal error"`, the panic value is
kept as the cause (`errors.Is` works if it was an error), the stack trace starts at the panic site, and
the log record has `panic` and `stack` attributes. `http.ErrAbortHandler` is re-panicked, as `net/http` expects.

### OpenTelemetry

`ErrorHandler` records failed requests on the span from the request context. The span must already exist,
so put `otelgrpc.NewServerHandler()` / `otelhttp.NewHandler` outside the handler (see [Quick Start](#quick-start)).
Nothing happens if the span is not recording.

| Error | Span attributes | Exception event | Span status |
|---|---|---|---|
| client errors (4xx), cancellations | `app.error.type`, `app.error.system_code`, `app.error.code` | no | unchanged |
| server-side failures (5xx incl. timeouts), panics | same | yes, with `exception.stacktrace` for internal errors and panics | `Error` |

This follows the OpenTelemetry semantic conventions: 4xx responses are not server errors, and recording
an exception for each of them would be noise under load. `otelgrpc` / `otelhttp` set the final span
status description when the span ends, so error details live in the exception event.

### Sentry

The optional `sentryreport` package reports to the Sentry hub bound to the request context
(falling back to the current hub). Stack traces of internal errors and panics are picked up automatically:

```go
apperror.WithReporter(sentryreport.Report)
```

---

## Client helpers

```go
// Option 1: check the type
if apperror.IsNotFound(err) { ... }

// Option 2: restore the full *AppError and compare with a sentinel
if ae, ok := apperror.FromGRPCError(err); ok && errors.Is(ae, ErrUserNotFound) { ... }

// Option 3: map ErrorInfo.Reason ("SYSTEMCODE-CODE") to domain errors
st, _ := status.FromError(err)
mappedErr := apperror.ErrorInfoFromDetails(st, map[string]func() error{
	"USERS-1": func() error { return ErrUserNotFound },
})
```

`FromGRPCStatus(st)` does the same as `FromGRPCError` for a `*status.Status`.
`FromError(err)` extracts a copy of `*AppError` from an error chain.

---

## Performance

Measured with Go 1.27 on x86-64 (`go test -bench`):

| Operation | Time | Allocations |
|---|---|---|
| `NewNotFoundError(...)` | 140 ns | 1 |
| `NewInternalError(...)` (captures the stack) | 690 ns | 2 |
| `IsNotFound(err)` on a wrapped `*AppError` | 4 ns | 0 |
| `IsNotFound(err)` on a gRPC client error | 2.3 µs | 15 |
| `Error()` with a cause | 310 ns | 4 |
| `GRPCStatus()` with field violations | 3 µs | 24 |
| unary interceptor, `NotFound` returned (JSON logger to `io.Discard`) | 3 µs | 13 |

---

## Testing

```bash
go test -race ./...
go test -run=^$ -fuzz=FuzzParseReason -fuzztime=30s .
```

---

## Best practices

- Declare sentinel errors with `WithCode` + `WithMessage` and compare them with `errors.Is`
- Wrap causes with `WithErr` and write client-facing text with `WithMessage`, never the other way around
- Use `WithTrace(ctx)` in handlers and services
- Use field violations instead of putting data in the message; give them a `Reason` if clients localize errors
- Set `WithRetryAfter` on `TooManyRequests` and `Unavailable` errors
- Return plain errors only for truly unexpected failures: the handler wraps and reports them
- Put OpenTelemetry instrumentation outside `ErrorHandler`; wrap routers that don't use `h.HTTP` with `h.HTTPMiddleware`
- On the client side use `IsNotFound`-style helpers or `FromGRPCError`

---

## License

[MIT License](https://github.com/jwm1rr0rb10/libraries/blob/main/backend/golang/LICENSE) – © Raman Zaitsau [@jwm1rrr0rb10](https://github.com/jwm1rr0rb10)
