package apperror

import (
	"fmt"
	"strings"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrorInfoFromDetails calls the matching handler based on ErrorInfo.Reason.
func ErrorInfoFromDetails(sErr *status.Status, reasonHandlers map[string]func() error) error {
	for _, detail := range sErr.Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if !ok {
			continue
		}

		if handler, ok := reasonHandlers[info.Reason]; ok {
			return handler()
		}
	}

	return nil
}

// FromGRPCError reconstructs *AppError from an error returned by a gRPC client (see FromGRPCStatus),
// so it can be compared with sentinel errors:
//
//	if ae, ok := apperror.FromGRPCError(err); ok && errors.Is(ae, ErrUserNotFound) { ... }
func FromGRPCError(err error) (*AppError, bool) {
	if err == nil {
		return nil, false
	}
	st, ok := status.FromError(err)
	if !ok {
		return nil, false
	}
	return FromGRPCStatus(st)
}

// FromGRPCStatus reconstructs *AppError from gRPC status (best-effort).
// Uses ErrorInfo to restore SystemCode, Code, Domain, Type and BadRequest field violations.
func FromGRPCStatus(s *status.Status) (*AppError, bool) {
	if s == nil {
		return nil, false
	}

	var (
		info       *errdetails.ErrorInfo
		violations []FieldViolation
		retryAfter time.Duration
	)

	for _, detail := range s.Details() {
		switch dt := detail.(type) {
		case *errdetails.ErrorInfo:
			info = dt
		case *errdetails.RetryInfo:
			retryAfter = dt.GetRetryDelay().AsDuration()
		case *errdetails.BadRequest:
			for _, v := range dt.FieldViolations {
				violations = append(violations, FieldViolation{
					Field:       v.GetField(),
					Reason:      v.GetReason(),
					Description: v.GetDescription(),
				})
			}
		}
	}

	if info == nil {
		return nil, false
	}

	systemCode, code := parseReason(info.Reason)

	ae := &AppError{
		Message:    s.Message(),
		SystemCode: systemCode,
		Code:       code,
		Domain:     info.Domain,
		Violations: violations,
		RetryAfter: retryAfter,
		Type:       typeFromMetadataOrGRPCCode(info.Metadata, s.Code()),
	}

	if traceID := info.Metadata["trace_id"]; traceID != "" {
		ae.TraceID = traceID
	}

	return ae, true
}

func parseReason(reason string) (systemCode string, code uint32) {
	if reason == "" {
		return "", 0
	}

	idx := strings.LastIndex(reason, "-")
	if idx <= 0 {
		return reason, 0
	}

	systemCode = reason[:idx]
	_, _ = fmt.Sscanf(reason[idx+1:], "%d", &code)
	return systemCode, code
}

func typeFromMetadataOrGRPCCode(metadata map[string]string, code codes.Code) Type {
	if metadata != nil {
		if typeName := metadata["error_type"]; typeName != "" {
			if t, ok := typeFromString(typeName); ok {
				return t
			}
		}
	}

	return typeFromGRPCCode(code)
}

func typeFromGRPCCode(code codes.Code) Type {
	switch code {
	case codes.NotFound:
		return TypeNotFound
	case codes.Unauthenticated:
		return TypeUnauthorized
	case codes.PermissionDenied:
		return TypeForbidden
	case codes.FailedPrecondition:
		return TypeConditionFailed
	case codes.InvalidArgument, codes.OutOfRange:
		return TypeBadRequest
	case codes.AlreadyExists, codes.Aborted:
		return TypeConflict
	case codes.ResourceExhausted:
		return TypeTooManyRequests
	case codes.Unavailable:
		return TypeUnavailable
	case codes.DeadlineExceeded:
		return TypeTimeout
	case codes.Canceled:
		return TypeCanceled
	case codes.Unimplemented:
		return TypeNotImplemented
	default:
		return TypeInternal
	}
}
