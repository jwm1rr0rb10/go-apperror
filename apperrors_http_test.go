package apperror

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func serveHTTP(t *testing.T, th *testHandler, handlerErr error) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("GET /users/{id}", th.HTTP(func(w http.ResponseWriter, r *http.Request) error { return handlerErr }))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/42", nil))

	var body map[string]any
	if handlerErr != nil {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	}
	return rec, body
}

func TestHTTPHandlerWritesAppError(t *testing.T) {
	th := newTestHandler()
	rec, body := serveHTTP(t, th, NewNotFoundError("SYS", WithCode(7)))

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, "NotFound", body["type"])
	assert.Equal(t, "not found", body["message"])
	assert.Equal(t, "GET /users/{id}", th.lastRecord(t)["method"])
	assert.Equal(t, "INFO", th.lastRecord(t)["level"])
}

func TestHTTPHandlerSuccess(t *testing.T) {
	rec, _ := serveHTTP(t, newTestHandler(), nil)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestHTTPHandlerUnknownError(t *testing.T) {
	th := newTestHandler()
	rec, body := serveHTTP(t, th, errors.New("pq: secret"))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "unknown internal system error", body["message"])
	assert.NotContains(t, rec.Body.String(), "pq: secret")
	assert.Len(t, th.reported, 1)
}

func TestHTTPHandlerContextCanceled(t *testing.T) {
	th := newTestHandler()
	rec, body := serveHTTP(t, th, NewInternalError("DB", WithErr(context.Canceled)))

	assert.Equal(t, 499, rec.Code)
	assert.Equal(t, "Canceled", body["type"])
	assert.Empty(t, th.reported)
}

func TestHTTPHandlerDownstreamGRPCErrors(t *testing.T) {
	downstream := NewConflictError("USERS", WithCode(3), WithMessage("email taken"))
	rec, body := serveHTTP(t, newTestHandler(), downstream.GRPCStatus().Err())
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "email taken", body["message"])
	assert.Equal(t, "USERS", body["system_code"])

	rec, body = serveHTTP(t, newTestHandler(), status.Error(codes.Unavailable, "dial tcp 10.0.0.1:5432"))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, "service unavailable", body["message"])
	assert.Equal(t, "SYS", body["system_code"])
}

func TestWriteHTTPErrorWithoutPattern(t *testing.T) {
	th := newTestHandler()
	rec := httptest.NewRecorder()
	th.WriteHTTPError(rec, httptest.NewRequest(http.MethodPost, "/raw", nil), NewForbiddenError("SYS"))

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "POST /raw", th.lastRecord(t)["method"])
}

func TestHTTPRetryAfterHeader(t *testing.T) {
	rec, _ := serveHTTP(t, newTestHandler(), NewTooManyRequestsError("SYS", WithRetryAfter(1500*time.Millisecond)))
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "2", rec.Header().Get("Retry-After"), "must round up to whole seconds")

	downstream := NewUnavailableError("DB", WithRetryAfter(3*time.Second)).GRPCStatus().Err()
	rec, _ = serveHTTP(t, newTestHandler(), downstream)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, "3", rec.Header().Get("Retry-After"), "retry delay from a downstream service must pass through")

	rec, _ = serveHTTP(t, newTestHandler(), NewUnavailableError("SYS"))
	assert.Empty(t, rec.Header().Get("Retry-After"))
}
