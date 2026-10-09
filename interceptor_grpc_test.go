package apperror

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type secretRequest struct{ Password string }

// testHandler records logs as JSON and collects reported errors.
type testHandler struct {
	*ErrorHandler
	logs     bytes.Buffer
	reported []error
}

func newTestHandler(opts ...HandlerOption) *testHandler {
	th := &testHandler{}
	logger := slog.New(slog.NewJSONHandler(&th.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	opts = append([]HandlerOption{
		WithLogger(func(context.Context) *slog.Logger { return logger }),
		WithReporter(func(_ context.Context, err error) { th.reported = append(th.reported, err) }),
	}, opts...)
	th.ErrorHandler = NewErrorHandler("SYS", opts...)
	return th
}

func (th *testHandler) lastRecord(t *testing.T) map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(th.logs.Bytes()), []byte("\n"))
	var record map[string]any
	require.NoError(t, json.Unmarshal(lines[len(lines)-1], &record))
	return record
}

func (th *testHandler) unary(t *testing.T, handlerErr error) error {
	t.Helper()
	handler := func(ctx context.Context, req any) (any, error) { return nil, handlerErr }

	_, err := th.UnaryServerInterceptor()(context.Background(), secretRequest{Password: "hunter2"}, &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}, handler)
	require.Error(t, err)
	return err
}

func TestGRPCUnaryInterceptorPreservesAppError(t *testing.T) {
	original := NewNotFoundError("SYS", WithCode(404), WithMessage("missing"))

	err := newTestHandler().unary(t, original)

	var appErr *AppError
	require.True(t, errors.As(err, &appErr))
	assert.Same(t, original, appErr)
}

func TestGRPCUnaryInterceptorWrapsUnknownError(t *testing.T) {
	th := newTestHandler()
	err := th.unary(t, errors.New("boom"))

	var appErr *AppError
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, "SYS", appErr.SystemCode)
	assert.Equal(t, TypeInternal, appErr.Type)
	assert.Equal(t, "unknown internal system error", appErr.Message)
	require.Len(t, th.reported, 1)
	assert.Same(t, appErr, th.reported[0])
}

func TestGRPCUnaryInterceptorPreservesNonUnknownStatus(t *testing.T) {
	original := status.Error(codes.InvalidArgument, "plain grpc")
	assert.Equal(t, original, newTestHandler().unary(t, original))
}

func TestGRPCUnaryInterceptorPassesSuccess(t *testing.T) {
	handler := func(ctx context.Context, req any) (any, error) { return "ok", nil }

	resp, err := GRPCUnaryInterceptor("SYS")(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}, handler)
	require.NoError(t, err)
	assert.Equal(t, "ok", resp)
}

func TestGRPCUnaryInterceptorContextErrors(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantCode  codes.Code
		wantLevel string
	}{
		{"canceled", context.Canceled, codes.Canceled, "INFO"},
		{"deadline", context.DeadlineExceeded, codes.DeadlineExceeded, "WARN"},
		{"wrapped canceled", fmt.Errorf("query: %w", context.Canceled), codes.Canceled, "INFO"},
		{"internal app error wrapping canceled", NewInternalError("DB", WithErr(context.Canceled)), codes.Canceled, "INFO"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			th := newTestHandler()
			err := th.unary(t, tt.err)

			assert.False(t, IsInternal(err), "context errors must not become internal errors")
			assert.Equal(t, tt.wantCode, status.Code(err))
			assert.Equal(t, tt.wantLevel, th.lastRecord(t)["level"])
			assert.Empty(t, th.reported)
		})
	}
}

func TestGRPCUnaryInterceptorLogLevelsAndReporting(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantLevel  string
		wantReport bool
	}{
		{"not found", NewNotFoundError("SYS"), "INFO", false},
		{"validation", NewValidationError("SYS"), "INFO", false},
		{"too many requests", NewTooManyRequestsError("SYS"), "INFO", false},
		{"timeout", NewTimeoutError("SYS"), "WARN", false},
		{"internal", NewInternalError("SYS"), "ERROR", true},
		{"unavailable", NewUnavailableError("SYS"), "ERROR", false},
		{"grpc invalid argument", status.Error(codes.InvalidArgument, "bad"), "INFO", false},
		{"grpc unavailable", status.Error(codes.Unavailable, "down"), "ERROR", false},
		{"unknown error", errors.New("boom"), "ERROR", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			th := newTestHandler()
			th.unary(t, tt.err)

			record := th.lastRecord(t)
			assert.Equal(t, tt.wantLevel, record["level"])
			assert.Equal(t, "/test.Service/Method", record["method"])
			assert.Equal(t, tt.wantReport, len(th.reported) == 1)
		})
	}
}

func TestGRPCUnaryInterceptorDoesNotLogRequestByDefault(t *testing.T) {
	th := newTestHandler()
	th.unary(t, errors.New("boom"))

	assert.NotContains(t, th.lastRecord(t), "request")
	assert.NotContains(t, th.logs.String(), "hunter2")
}

func TestGRPCUnaryInterceptorWithRequestAttr(t *testing.T) {
	th := newTestHandler(WithRequestAttr(func(req any) slog.Attr { return slog.String("request", "redacted") }))
	th.unary(t, errors.New("boom"))

	assert.Equal(t, "redacted", th.lastRecord(t)["request"])
}

func TestNewErrorHandlerDefaults(t *testing.T) {
	h := NewErrorHandler("SYS")
	assert.Equal(t, slog.Default(), h.logger(context.Background()))
	assert.NotPanics(t, func() { h.reportErr(context.Background(), errors.New("boom")) })
}

type testServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s testServerStream) Context() context.Context { return s.ctx }

func TestGRPCStreamInterceptor(t *testing.T) {
	th := newTestHandler()
	stream := testServerStream{ctx: context.Background()}
	info := &grpc.StreamServerInfo{FullMethod: "/test.Service/Stream"}

	err := th.StreamServerInterceptor()(nil, stream, info, func(any, grpc.ServerStream) error { return context.Canceled })
	assert.Equal(t, codes.Canceled, status.Code(err))
	assert.Equal(t, "/test.Service/Stream", th.lastRecord(t)["method"])

	err = th.StreamServerInterceptor()(nil, stream, info, func(any, grpc.ServerStream) error { return errors.New("boom") })
	assert.True(t, IsInternal(err))
	assert.Len(t, th.reported, 1)

	err = GRPCStreamInterceptor("SYS")(nil, stream, info, func(any, grpc.ServerStream) error { return nil })
	assert.NoError(t, err)
}
