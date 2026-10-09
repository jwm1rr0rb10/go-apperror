package apperror

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/status"
)

func TestErrorFromDetails(t *testing.T) {
	var want int
	var want2 int

	var ErrSome = errors.New("some error")

	reasonHandlers := map[string]func() error{
		"PS-401": func() error {
			want = 401

			return nil
		},
		"PS-404": func() error {
			want2 = 404

			return nil
		},
		"PS-500": func() error {
			return ErrSome
		},
	}

	// 401
	foundError := NewNotFoundError("PS", WithCode(401), WithMessage("не хватило баланса"), WithDomain("PS"))
	sErr, ok := status.FromError(foundError)
	if !ok {
		panic("not a status error")
	}

	err := ErrorInfoFromDetails(sErr, reasonHandlers)
	assert.NoError(t, err)

	assert.Equal(t, want, 401, "not 401")

	// 404
	foundError = NewNotFoundError("PS", WithCode(404), WithMessage("не хватило баланса"), WithDomain("PS"))
	sErr, ok = status.FromError(foundError)
	if !ok {
		panic("not a status error")
	}

	err = ErrorInfoFromDetails(sErr, reasonHandlers)
	assert.NoError(t, err)

	assert.Equal(t, want2, 404, "not 401")

	// 500 -- error

	foundError = NewNotFoundError("PS", WithCode(500), WithMessage("не хватило баланса"), WithDomain("PS"))
	sErr, ok = status.FromError(foundError)
	if !ok {
		panic("not a status error")
	}

	err = ErrorInfoFromDetails(sErr, reasonHandlers)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrSome))
}
