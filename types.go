package apperror

import (
	"fmt"
	"net/http"

	"google.golang.org/grpc/codes"
)

// Type classifies the category of error and maps to appropriate gRPC and HTTP status codes.
type Type uint16

// Error types. New values must be appended to keep numeric values stable.
const (
	TypeInternal Type = iota
	TypeBadRequest
	TypeValidation
	TypeNotFound
	TypeUnauthorized
	TypeForbidden
	TypeConditionFailed
	TypeConflict
	TypeTooManyRequests
	TypeUnavailable
	TypeTimeout
	TypeCanceled
	TypeNotImplemented
)

// statusClientClosedRequest is the non-standard (nginx) status for requests canceled by the client.
const statusClientClosedRequest = 499

type typeInfo struct {
	name       string
	httpStatus int
	grpcCode   codes.Code
	message    string // safe default message exposed to clients
}

var types = [...]typeInfo{
	TypeInternal:        {"InternalSystem", http.StatusInternalServerError, codes.Internal, "internal error"},
	TypeBadRequest:      {"BadRequest", http.StatusBadRequest, codes.InvalidArgument, "bad request"},
	TypeValidation:      {"Validation", http.StatusBadRequest, codes.InvalidArgument, "validation failed"},
	TypeNotFound:        {"NotFound", http.StatusNotFound, codes.NotFound, "not found"},
	TypeUnauthorized:    {"Unauthorized", http.StatusUnauthorized, codes.Unauthenticated, "unauthorized"},
	TypeForbidden:       {"Forbidden", http.StatusForbidden, codes.PermissionDenied, "forbidden"},
	TypeConditionFailed: {"ConditionFailed", http.StatusPreconditionFailed, codes.FailedPrecondition, "precondition failed"},
	TypeConflict:        {"Conflict", http.StatusConflict, codes.AlreadyExists, "conflict"},
	TypeTooManyRequests: {"TooManyRequests", http.StatusTooManyRequests, codes.ResourceExhausted, "too many requests"},
	TypeUnavailable:     {"Unavailable", http.StatusServiceUnavailable, codes.Unavailable, "service unavailable"},
	TypeTimeout:         {"Timeout", http.StatusGatewayTimeout, codes.DeadlineExceeded, "timeout"},
	TypeCanceled:        {"Canceled", statusClientClosedRequest, codes.Canceled, "request canceled"},
	TypeNotImplemented:  {"NotImplemented", http.StatusNotImplemented, codes.Unimplemented, "not implemented"},
}

func (t Type) valid() bool {
	return int(t) < len(types)
}

// String returns a human-readable name for the error type (useful for logging).
func (t Type) String() string {
	if !t.valid() {
		return fmt.Sprintf("UnknownType(%d)", t)
	}
	return types[t].name
}

// HTTPStatus returns the corresponding HTTP status code for this error type.
func (t Type) HTTPStatus() int {
	if !t.valid() {
		return http.StatusInternalServerError
	}
	return types[t].httpStatus
}

// GRPCCode returns the corresponding gRPC status code for this error type.
func (t Type) GRPCCode() codes.Code {
	if !t.valid() {
		return codes.Unknown
	}
	return types[t].grpcCode
}

func (t Type) defaultMessage() string {
	if !t.valid() {
		return types[TypeInternal].message
	}
	return types[t].message
}

func (t Type) Uint32() uint32 {
	return uint32(t)
}

// MarshalText implements encoding.TextMarshaler, so JSON represents the type by name.
func (t Type) MarshalText() ([]byte, error) {
	return []byte(t.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (t *Type) UnmarshalText(text []byte) error {
	parsed, ok := typeFromString(string(text))
	if !ok {
		return fmt.Errorf("apperror: unknown error type %q", text)
	}
	*t = parsed
	return nil
}

func typeFromString(name string) (Type, bool) {
	for t, info := range types {
		if info.name == name {
			return Type(t), true
		}
	}
	return 0, false
}
