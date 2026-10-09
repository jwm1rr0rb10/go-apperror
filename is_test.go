package apperror

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestTypeOf(t *testing.T) {
	typ, ok := TypeOf(fmt.Errorf("repo: %w", NewNotFoundError("PS")))
	assert.True(t, ok)
	assert.Equal(t, TypeNotFound, typ)

	_, ok = TypeOf(errors.New("plain"))
	assert.False(t, ok)

	_, ok = TypeOf(nil)
	assert.False(t, ok)
}

func TestIsHelpers(t *testing.T) {
	tests := []struct {
		constructor func(string, ...Option) *AppError
		is          func(error) bool
	}{
		{NewInternalError, IsInternal},
		{NewBadRequestError, IsBadRequest},
		{NewValidationError, IsValidation},
		{NewNotFoundError, IsNotFound},
		{NewUnauthorizedError, IsUnauthorized},
		{NewForbiddenError, IsForbidden},
		{NewConditionFailedError, IsConditionFailed},
		{NewConflictError, IsConflict},
		{NewTooManyRequestsError, IsTooManyRequests},
		{NewUnavailableError, IsUnavailable},
		{NewTimeoutError, IsTimeout},
		{NewCanceledError, IsCanceled},
		{NewNotImplementedError, IsNotImplemented},
	}

	for i, tt := range tests {
		err := fmt.Errorf("wrapped: %w", tt.constructor("PS"))
		assert.True(t, tt.is(err), "helper %d", i)

		other := tests[(i+1)%len(tests)].constructor("PS")
		assert.False(t, tt.is(other), "helper %d must not match another type", i)
		assert.False(t, tt.is(errors.New("plain")))
	}
}

func TestTypeOfGRPCErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want Type
	}{
		{"restored app error keeps validation type", NewValidationError("USERS").GRPCStatus().Err(), TypeValidation},
		{"wrapped grpc error", fmt.Errorf("call: %w", NewConflictError("USERS").GRPCStatus().Err()), TypeConflict},
		{"plain status by code", status.Error(codes.NotFound, "x"), TypeNotFound},
		{"plain status unavailable", status.Error(codes.Unavailable, "x"), TypeUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := TypeOf(tt.err)
			assert.True(t, ok)
			assert.Equal(t, tt.want, got)
		})
	}

	assert.True(t, IsNotFound(status.Error(codes.NotFound, "x")))
	assert.False(t, IsNotFound(status.Error(codes.Internal, "x")))
}
