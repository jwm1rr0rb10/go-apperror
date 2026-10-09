package apperror

import (
	"errors"

	"google.golang.org/grpc/status"
)

// TypeOf returns the type of err: of the first *AppError in its chain or, for errors
// returned by gRPC clients, of the restored *AppError or the gRPC status code.
func TypeOf(err error) (Type, bool) {
	if err == nil {
		return 0, false
	}
	if appErr, ok := errors.AsType[*AppError](err); ok {
		return appErr.Type, true
	}

	st, ok := status.FromError(err)
	if !ok {
		return 0, false
	}
	if restored, ok := FromGRPCStatus(st); ok {
		return restored.Type, true
	}
	return typeFromGRPCCode(st.Code()), true
}

// IsType reports whether err is of type t (see TypeOf), including errors from gRPC clients.
func IsType(err error, t Type) bool {
	got, ok := TypeOf(err)
	return ok && got == t
}

func IsInternal(err error) bool        { return IsType(err, TypeInternal) }
func IsBadRequest(err error) bool      { return IsType(err, TypeBadRequest) }
func IsValidation(err error) bool      { return IsType(err, TypeValidation) }
func IsNotFound(err error) bool        { return IsType(err, TypeNotFound) }
func IsUnauthorized(err error) bool    { return IsType(err, TypeUnauthorized) }
func IsForbidden(err error) bool       { return IsType(err, TypeForbidden) }
func IsConditionFailed(err error) bool { return IsType(err, TypeConditionFailed) }
func IsConflict(err error) bool        { return IsType(err, TypeConflict) }
func IsTooManyRequests(err error) bool { return IsType(err, TypeTooManyRequests) }
func IsUnavailable(err error) bool     { return IsType(err, TypeUnavailable) }
func IsTimeout(err error) bool         { return IsType(err, TypeTimeout) }
func IsCanceled(err error) bool        { return IsType(err, TypeCanceled) }
func IsNotImplemented(err error) bool  { return IsType(err, TypeNotImplemented) }
