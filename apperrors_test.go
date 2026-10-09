package apperror

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWithFieldsDoesNotMutateOriginal(t *testing.T) {
	original := NewValidationError("PS", WithMessage("invalid input"), WithFields(ErrorFields{"name": "required"}))
	updated := original.WithFields(ErrorFields{"email": "required"})

	assert.Equal(t, []FieldViolation{{Field: "name", Description: "required"}}, original.Violations)
	assert.Equal(t, []FieldViolation{
		{Field: "name", Description: "required"},
		{Field: "email", Description: "required"},
	}, updated.Violations, "WithFields must append to existing violations")
}

func TestWithFieldsOptionSortsAndDoesNotAliasMap(t *testing.T) {
	fields := ErrorFields{"b": "2", "a": "1", "c": "3"}
	err := NewValidationError("PS", WithFields(fields))
	fields["a"] = "changed"

	assert.Equal(t, []FieldViolation{
		{Field: "a", Description: "1"},
		{Field: "b", Description: "2"},
		{Field: "c", Description: "3"},
	}, err.Violations)
}

func TestWithViolations(t *testing.T) {
	err := NewValidationError("PS",
		WithViolations(FieldViolation{Field: "password", Reason: "TOO_SHORT", Description: "min 8 chars"}),
	).WithViolations(FieldViolation{Field: "password", Reason: "NO_DIGITS", Description: "must contain a digit"})

	assert.Equal(t, []FieldViolation{
		{Field: "password", Reason: "TOO_SHORT", Description: "min 8 chars"},
		{Field: "password", Reason: "NO_DIGITS", Description: "must contain a digit"},
	}, err.Violations)
}

func TestStackTraceOnlyForInternal(t *testing.T) {
	err := NewInternalError("PS")
	require.NotEmpty(t, err.StackTrace())

	frame, _ := runtime.CallersFrames(err.StackTrace()).Next()
	assert.Equal(t, "kalipso/app/pkg/golang/apperror.TestStackTraceOnlyForInternal", frame.Function,
		"first frame must be the caller of the constructor")

	assert.Empty(t, NewNotFoundError("PS").StackTrace())
	assert.Equal(t, err.StackTrace(), err.WithTrace(context.Background()).StackTrace())
}

func TestWithTraceDoesNotMutateOriginal(t *testing.T) {
	original := NewBadRequestError("PS", WithMessage("bad"))
	traceID := trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
	}))

	updated := original.WithTrace(ctx)

	assert.Empty(t, original.TraceID)
	assert.Equal(t, traceID.String(), updated.TraceID)
}

func TestWithTraceOption(t *testing.T) {
	traceID := trace.TraceID{16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
	}))

	err := NewNotFoundError("PS", WithCode(404), WithMessage("missing"), WithTrace(ctx))
	assert.Equal(t, traceID.String(), err.TraceID)
}

func TestHTTPErrorReturnsCopy(t *testing.T) {
	original := NewNotFoundError("PS", WithCode(404), WithFields(ErrorFields{"id": "1"}))
	copy := original.HTTPError()

	copy.Violations[0].Description = "2"
	assert.Equal(t, "1", original.Violations[0].Description)
}

func TestFromError(t *testing.T) {
	base := NewForbiddenError("AUTH", WithCode(403), WithMessage("denied"))
	wrapped := fmt.Errorf("wrapped: %w", base)

	got, ok := FromError(wrapped)
	require.True(t, ok)
	assert.Equal(t, uint32(403), got.Code)
	assert.Equal(t, "denied", got.Message)
	assert.NotSame(t, base, got)
}

func TestErrorsIsAndAs(t *testing.T) {
	err := NewNotFoundError("PS", WithCode(404))
	target := NewNotFoundError("PS", WithCode(404))

	assert.True(t, errors.Is(err, target))
	assert.False(t, errors.Is(err, NewNotFoundError("PS", WithCode(405))))

	var appErr *AppError
	assert.True(t, errors.As(err, &appErr))
	assert.Equal(t, TypeNotFound, appErr.Type)
}

func TestHTTPStatusByType(t *testing.T) {
	tests := []struct {
		name        string
		constructor func(string, ...Option) *AppError
		wantStatus  int
	}{
		{"internal", NewInternalError, http.StatusInternalServerError},
		{"bad request", NewBadRequestError, http.StatusBadRequest},
		{"validation", NewValidationError, http.StatusBadRequest},
		{"not found", NewNotFoundError, http.StatusNotFound},
		{"unauthorized", NewUnauthorizedError, http.StatusUnauthorized},
		{"forbidden", NewForbiddenError, http.StatusForbidden},
		{"condition failed", NewConditionFailedError, http.StatusPreconditionFailed},
		{"conflict", NewConflictError, http.StatusConflict},
		{"too many requests", NewTooManyRequestsError, http.StatusTooManyRequests},
		{"unavailable", NewUnavailableError, http.StatusServiceUnavailable},
		{"timeout", NewTimeoutError, http.StatusGatewayTimeout},
		{"canceled", NewCanceledError, 499},
		{"not implemented", NewNotImplementedError, http.StatusNotImplemented},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.constructor("PS", WithMessage("msg"))
			assert.Equal(t, tt.wantStatus, err.HTTPStatus())
		})
	}
}

func TestValidationGRPCStatusRoundTripPreservesType(t *testing.T) {
	original := NewValidationError(
		"MY-SVC",
		WithCode(422),
		WithMessage("validation failed"),
		WithFields(ErrorFields{"email": "invalid"}),
	)

	restored, ok := FromGRPCStatus(original.GRPCStatus())
	require.True(t, ok)
	assert.Equal(t, TypeValidation, restored.Type)
}

func TestGRPCStatusRoundTrip(t *testing.T) {
	original := NewValidationError(
		"MY-SVC",
		WithCode(422),
		WithMessage("validation failed"),
		WithDomain("users"),
		WithFields(ErrorFields{
			"email": "invalid format",
			"name":  "required",
		}),
		WithTraceID("trace-123"),
	)

	st := original.GRPCStatus()
	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Equal(t, "validation failed", st.Message())

	restored, ok := FromGRPCStatus(st)
	require.True(t, ok)
	assert.Equal(t, "MY-SVC", restored.SystemCode)
	assert.Equal(t, uint32(422), restored.Code)
	assert.Equal(t, "users", restored.Domain)
	assert.Equal(t, "trace-123", restored.TraceID)
	assert.Equal(t, TypeValidation, restored.Type)
	assert.Equal(t, []FieldViolation{
		{Field: "email", Description: "invalid format"},
		{Field: "name", Description: "required"},
	}, restored.Violations)

	withReason := NewValidationError("PS", WithViolations(FieldViolation{Field: "age", Reason: "OUT_OF_RANGE", Description: "18+"}))
	restored, ok = FromGRPCStatus(withReason.GRPCStatus())
	require.True(t, ok)
	assert.Equal(t, withReason.Violations, restored.Violations)
}

func TestParseReason(t *testing.T) {
	tests := []struct {
		reason     string
		systemCode string
		code       uint32
	}{
		{"PS-404", "PS", 404},
		{"MY-SVC-422", "MY-SVC", 422},
		{"UNKNOWN", "UNKNOWN", 0},
		{"", "", 0},
	}

	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			systemCode, code := parseReason(tt.reason)
			assert.Equal(t, tt.systemCode, systemCode)
			assert.Equal(t, tt.code, code)
		})
	}
}

func TestMarshalJSON(t *testing.T) {
	err := NewNotFoundError("PS", WithCode(404), WithMessage("missing"), WithTraceID("abc"))
	data, marshalErr := err.MarshalJSON()
	require.NoError(t, marshalErr)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(data, &payload))
	assert.Equal(t, "missing", payload["message"])
	assert.Equal(t, "PS", payload["system_code"])
	assert.Equal(t, float64(404), payload["code"])
	assert.Equal(t, "abc", payload["trace_id"])
	_, hasErrField := payload["err"]
	assert.False(t, hasErrField)
}

func TestGRPCStatusIncludesErrorInfoAndBadRequest(t *testing.T) {
	err := NewBadRequestError("PS", WithCode(400), WithMessage("bad"), WithFields(ErrorFields{"id": "invalid"}))
	st := err.GRPCStatus()

	var hasErrorInfo bool
	var hasBadRequest bool
	for _, detail := range st.Details() {
		switch detail.(type) {
		case *errdetails.ErrorInfo:
			hasErrorInfo = true
		case *errdetails.BadRequest:
			hasBadRequest = true
		}
	}

	assert.True(t, hasErrorInfo)
	assert.True(t, hasBadRequest)
}

func TestTypeString(t *testing.T) {
	assert.Equal(t, "NotFound", TypeNotFound.String())
	assert.Equal(t, "Validation", TypeValidation.String())
	assert.Equal(t, "UnknownType(99)", Type(99).String())
}

func TestTypeUint32(t *testing.T) {
	assert.Equal(t, uint32(TypeNotFound), TypeNotFound.Uint32())
}

func TestNewAppErrorDefaults(t *testing.T) {
	err := newAppError(TypeBadRequest, "", WithMessage("bad"))
	assert.Equal(t, "UNKNOWN", err.SystemCode)
	assert.Equal(t, "bad", err.Message)
}

func TestWithErrDoesNotExposeCause(t *testing.T) {
	root := errors.New("pq: password authentication failed for user admin")
	err := NewInternalError("SYS", WithErr(root))

	assert.Equal(t, "internal error", err.Message)
	assert.True(t, errors.Is(err, root))
	assert.Contains(t, err.Error(), root.Error(), "cause must stay visible in logs")
	assert.NotContains(t, string(err.Marshal()), "pq:")
	assert.Equal(t, "internal error", err.GRPCStatus().Message())
}

func TestDefaultMessagePerType(t *testing.T) {
	assert.Equal(t, "not found", NewNotFoundError("PS").Message)
	assert.Equal(t, "too many requests", NewTooManyRequestsError("PS").Message)
	assert.Equal(t, "custom", NewNotFoundError("PS", WithMessage("custom")).Message)
	assert.Equal(t, "internal error", newAppError(Type(99), "PS").Message)
}

func TestErrorStringOmitsDuplicateCause(t *testing.T) {
	err := NewNotFoundError("PS", WithMessage("missing"), WithErr(errors.New("missing")))
	assert.Equal(t, "missing (code: PS-0)", err.Error())

	err = NewNotFoundError("PS", WithMessage("missing"), WithErr(sql.ErrNoRows))
	assert.Equal(t, "missing: sql: no rows in result set (code: PS-0)", err.Error())
}

func TestLogValueIncludesCause(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	err := NewInternalError("PS", WithCode(7), WithErr(errors.New("db down")), WithFields(ErrorFields{"id": "1"}), WithRetryAfter(time.Second))

	logger.Error("failed", slog.Any("error", err))

	var record struct {
		Error map[string]any `json:"error"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &record))
	assert.Equal(t, "internal error", record.Error["message"])
	assert.Equal(t, "db down", record.Error["cause"])
	assert.Equal(t, float64(time.Second), record.Error["retry_after"])
	assert.Equal(t, "InternalSystem", record.Error["type"])
	assert.Equal(t, float64(7), record.Error["code"])
	assert.Equal(t, []any{map[string]any{"field": "id", "description": "1"}}, record.Error["violations"])
}

func TestErrorStringExcludesHighCardinalityData(t *testing.T) {
	err := NewNotFoundError(
		"PS",
		WithCode(404),
		WithMessage("missing"),
		WithFields(ErrorFields{"id": "1"}),
		WithTraceID("trace-1"),
	)

	assert.Equal(t, "missing (code: PS-404)", err.Error(),
		"fields and trace ID must not leak into the error string (breaks grouping)")
}

func TestMarshalReturnsJSONBytes(t *testing.T) {
	err := NewBadRequestError("PS", WithCode(400), WithMessage("bad"))
	data := err.Marshal()
	require.NotEmpty(t, data)
	assert.Contains(t, string(data), `"message":"bad"`)
}

func TestFromGRPCStatusNil(t *testing.T) {
	restored, ok := FromGRPCStatus(nil)
	assert.False(t, ok)
	assert.Nil(t, restored)
}

func TestFromGRPCStatusWithoutErrorInfo(t *testing.T) {
	st := status.New(codes.Internal, "boom")
	restored, ok := FromGRPCStatus(st)
	assert.False(t, ok)
	assert.Nil(t, restored)
}

func TestGRPCCodesByType(t *testing.T) {
	tests := []struct {
		name        string
		constructor func(string, ...Option) *AppError
		wantCode    codes.Code
	}{
		{"internal", NewInternalError, codes.Internal},
		{"bad request", NewBadRequestError, codes.InvalidArgument},
		{"validation", NewValidationError, codes.InvalidArgument},
		{"not found", NewNotFoundError, codes.NotFound},
		{"unauthorized", NewUnauthorizedError, codes.Unauthenticated},
		{"forbidden", NewForbiddenError, codes.PermissionDenied},
		{"condition failed", NewConditionFailedError, codes.FailedPrecondition},
		{"conflict", NewConflictError, codes.AlreadyExists},
		{"too many requests", NewTooManyRequestsError, codes.ResourceExhausted},
		{"unavailable", NewUnavailableError, codes.Unavailable},
		{"timeout", NewTimeoutError, codes.DeadlineExceeded},
		{"canceled", NewCanceledError, codes.Canceled},
		{"not implemented", NewNotImplementedError, codes.Unimplemented},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.constructor("PS", WithMessage("msg"))
			assert.Equal(t, tt.wantCode, err.GRPCStatus().Code())
		})
	}
}

func TestTypeFromGRPCCodeFallback(t *testing.T) {
	assert.Equal(t, TypeInternal, typeFromGRPCCode(codes.DataLoss))
	assert.Equal(t, TypeInternal, typeFromGRPCCode(codes.Unknown))
}

func TestTypeFromStringAllValues(t *testing.T) {
	tests := map[string]Type{
		"InternalSystem":  TypeInternal,
		"BadRequest":      TypeBadRequest,
		"Validation":      TypeValidation,
		"NotFound":        TypeNotFound,
		"Unauthorized":    TypeUnauthorized,
		"Forbidden":       TypeForbidden,
		"ConditionFailed": TypeConditionFailed,
		"Conflict":        TypeConflict,
		"TooManyRequests": TypeTooManyRequests,
		"Unavailable":     TypeUnavailable,
		"Timeout":         TypeTimeout,
		"Canceled":        TypeCanceled,
		"NotImplemented":  TypeNotImplemented,
	}

	for name, want := range tests {
		got, ok := typeFromString(name)
		assert.True(t, ok)
		assert.Equal(t, want, got)
	}

	_, ok := typeFromString("Unknown")
	assert.False(t, ok)
}

func TestTypeFromGRPCCodeAllValues(t *testing.T) {
	assert.Equal(t, TypeNotFound, typeFromGRPCCode(codes.NotFound))
	assert.Equal(t, TypeUnauthorized, typeFromGRPCCode(codes.Unauthenticated))
	assert.Equal(t, TypeForbidden, typeFromGRPCCode(codes.PermissionDenied))
	assert.Equal(t, TypeConditionFailed, typeFromGRPCCode(codes.FailedPrecondition))
	assert.Equal(t, TypeBadRequest, typeFromGRPCCode(codes.InvalidArgument))
	assert.Equal(t, TypeInternal, typeFromGRPCCode(codes.Internal))
	assert.Equal(t, TypeBadRequest, typeFromGRPCCode(codes.OutOfRange))
	assert.Equal(t, TypeConflict, typeFromGRPCCode(codes.AlreadyExists))
	assert.Equal(t, TypeConflict, typeFromGRPCCode(codes.Aborted))
	assert.Equal(t, TypeTooManyRequests, typeFromGRPCCode(codes.ResourceExhausted))
	assert.Equal(t, TypeUnavailable, typeFromGRPCCode(codes.Unavailable))
	assert.Equal(t, TypeTimeout, typeFromGRPCCode(codes.DeadlineExceeded))
	assert.Equal(t, TypeCanceled, typeFromGRPCCode(codes.Canceled))
	assert.Equal(t, TypeNotImplemented, typeFromGRPCCode(codes.Unimplemented))
}

func TestGRPCStatusRoundTripAllTypes(t *testing.T) {
	for typ := range len(types) {
		original := newAppError(Type(typ), "PS", WithCode(1))
		restored, ok := FromGRPCStatus(original.GRPCStatus())
		require.True(t, ok)
		assert.Equal(t, Type(typ), restored.Type, Type(typ).String())
	}
}

func TestTypeJSONIsString(t *testing.T) {
	data := NewConflictError("PS", WithMessage("dup")).Marshal()
	assert.Contains(t, string(data), `"type":"Conflict"`)

	var decoded AppError
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, TypeConflict, decoded.Type)

	assert.Error(t, json.Unmarshal([]byte(`{"type":"Nope"}`), &decoded))
}

func TestFromErrorNil(t *testing.T) {
	got, ok := FromError(nil)
	assert.False(t, ok)
	assert.Nil(t, got)
}

func TestFromErrorDirectHTTPError(t *testing.T) {
	err := NewBadRequestError("PS", WithCode(400))
	got, ok := FromError(err)
	require.True(t, ok)
	assert.Equal(t, uint32(400), got.Code)
}

func TestErrorInfoFromDetailsNoMatch(t *testing.T) {
	err := NewNotFoundError("PS", WithCode(999))
	st, ok := status.FromError(err)
	require.True(t, ok)

	got := ErrorInfoFromDetails(st, map[string]func() error{})
	assert.Nil(t, got)
}

func TestAppErrorIsNilTarget(t *testing.T) {
	err := NewNotFoundError("PS", WithCode(404))
	assert.False(t, err.Is(nil))
}

func TestCloneAndWithFieldsNilReceiver(t *testing.T) {
	var err *AppError
	assert.Nil(t, err.clone())
	assert.Nil(t, err.WithFields(ErrorFields{"a": "b"}))
	assert.Nil(t, err.WithTrace(context.Background()))
}

func TestErrorStringMessageOnly(t *testing.T) {
	err := &AppError{Message: "solo", Err: errors.New("solo")}
	assert.Equal(t, "solo", err.Error())
}

func TestHTTPStatusUnknownType(t *testing.T) {
	assert.Equal(t, http.StatusInternalServerError, Type(99).HTTPStatus())
}

func TestIsWithoutCodeComparesMessage(t *testing.T) {
	errUser := NewNotFoundError("USERS", WithMessage("user not found"))
	errOrder := NewNotFoundError("USERS", WithMessage("order not found"))

	assert.False(t, errors.Is(errUser, errOrder), "different errors without a code must not match")
	assert.True(t, errors.Is(errUser.WithTrace(context.Background()), errUser), "copies of a sentinel must match")

	restored, ok := FromGRPCStatus(errUser.GRPCStatus())
	require.True(t, ok)
	assert.True(t, errors.Is(restored, errUser), "restored error must match the sentinel")

	withCode := NewNotFoundError("USERS", WithCode(1), WithMessage("user not found"))
	assert.True(t, errors.Is(NewNotFoundError("USERS", WithCode(1), WithMessage("other text")), withCode),
		"errors with a code match regardless of message")
	assert.False(t, errUser.Is((*AppError)(nil)))
}

func TestErrorCauseIsOneLine(t *testing.T) {
	err := NewInternalError("SYS", WithErr(errors.Join(errors.New("timeout"), errors.New("dial: refused"))))
	assert.Equal(t, "internal error: timeout; dial: refused (code: SYS-0)", err.Error())
}

func TestRetryAfterGRPCRoundTrip(t *testing.T) {
	original := NewUnavailableError("SYS", WithRetryAfter(1500*time.Millisecond))

	var hasRetryInfo bool
	for _, detail := range original.GRPCStatus().Details() {
		if info, ok := detail.(*errdetails.RetryInfo); ok {
			hasRetryInfo = true
			assert.Equal(t, 1500*time.Millisecond, info.GetRetryDelay().AsDuration())
		}
	}
	assert.True(t, hasRetryInfo)

	restored, ok := FromGRPCStatus(original.GRPCStatus())
	require.True(t, ok)
	assert.Equal(t, 1500*time.Millisecond, restored.RetryAfter)
	assert.NotContains(t, string(original.Marshal()), "retry")
}

func TestFromGRPCError(t *testing.T) {
	_, ok := FromGRPCError(nil)
	assert.False(t, ok)

	_, ok = FromGRPCError(errors.New("plain"))
	assert.False(t, ok)

	_, ok = FromGRPCError(status.Error(codes.NotFound, "no details"))
	assert.False(t, ok)

	sentinel := NewNotFoundError("USERS", WithCode(1))
	restored, ok := FromGRPCError(fmt.Errorf("call users: %w", sentinel.GRPCStatus().Err()))
	require.True(t, ok)
	assert.True(t, errors.Is(restored, sentinel))
}
