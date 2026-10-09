package apperror

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func firstFrame(t *testing.T, err error) string {
	t.Helper()
	appErr, ok := FromError(err)
	require.True(t, ok)
	require.NotEmpty(t, appErr.StackTrace())
	frame, _ := runtime.CallersFrames(appErr.StackTrace()).Next()
	return frame.Function
}

func TestUnaryInterceptorRecoversPanic(t *testing.T) {
	th := newTestHandler()
	handler := func(ctx context.Context, req any) (any, error) { panic("boom") }

	resp, err := th.UnaryServerInterceptor()(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}, handler)

	assert.Nil(t, resp)
	require.True(t, IsInternal(err))
	assert.Equal(t, codes.Internal, status.Code(err))
	assert.Equal(t, "internal error", status.Convert(err).Message(), "panic value must not reach the client")
	assert.Contains(t, firstFrame(t, err), "TestUnaryInterceptorRecoversPanic", "stack must start at the panic site")

	record := th.lastRecord(t)
	assert.Equal(t, "ERROR", record["level"])
	assert.Equal(t, "boom", record["panic"])
	assert.Contains(t, record["stack"], "TestUnaryInterceptorRecoversPanic")
	require.Len(t, th.reported, 1)
	assert.Same(t, err, th.reported[0])
}

func TestPanicWithErrorKeepsChain(t *testing.T) {
	sentinel := errors.New("sentinel")
	th := newTestHandler()
	handler := func(ctx context.Context, req any) (any, error) { panic(sentinel) }

	_, err := th.UnaryServerInterceptor()(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/m"}, handler)

	assert.True(t, errors.Is(err, sentinel))
	assert.Contains(t, err.Error(), "panic: sentinel")
}

func TestPanicRuntimeErrorStackStartsAtUserCode(t *testing.T) {
	th := newTestHandler()
	handler := func(ctx context.Context, req any) (any, error) {
		var m map[string]int
		m["x"] = 1 // assignment to a nil map panics in the runtime
		return nil, nil
	}

	_, err := th.UnaryServerInterceptor()(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/m"}, handler)

	var runtimeErr runtime.Error
	assert.True(t, errors.As(err, &runtimeErr))
	assert.Contains(t, firstFrame(t, err), "TestPanicRuntimeErrorStackStartsAtUserCode")
}

func TestStreamInterceptorRecoversPanic(t *testing.T) {
	th := newTestHandler()
	stream := testServerStream{ctx: context.Background()}

	err := th.StreamServerInterceptor()(nil, stream, &grpc.StreamServerInfo{FullMethod: "/test.Service/Stream"}, func(any, grpc.ServerStream) error {
		panic("stream boom")
	})

	assert.True(t, IsInternal(err))
	assert.Equal(t, "stream boom", th.lastRecord(t)["panic"])
	assert.Len(t, th.reported, 1)
}

func TestHTTPRecoversPanic(t *testing.T) {
	tests := map[string]func(th *testHandler) http.Handler{
		"adapter": func(th *testHandler) http.Handler {
			return th.HTTP(func(w http.ResponseWriter, r *http.Request) error { panic("http boom") })
		},
		"middleware": func(th *testHandler) http.Handler {
			return th.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("http boom") }))
		},
	}

	for name, build := range tests {
		t.Run(name, func(t *testing.T) {
			th := newTestHandler()
			rec := httptest.NewRecorder()
			build(th).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

			assert.Equal(t, http.StatusInternalServerError, rec.Code)
			assert.JSONEq(t, `{"message":"internal error","system_code":"SYS","type":"InternalSystem","code":0}`, rec.Body.String())
			assert.Equal(t, "http boom", th.lastRecord(t)["panic"])
			assert.Len(t, th.reported, 1)
		})
	}
}

func TestHTTPMiddlewarePassesThrough(t *testing.T) {
	th := newTestHandler()
	rec := httptest.NewRecorder()
	th.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	assert.Equal(t, http.StatusAccepted, rec.Code)
	assert.Empty(t, th.logs.String())
}

func TestHTTPAbortHandlerIsRepanicked(t *testing.T) {
	th := newTestHandler()
	handler := th.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	assert.PanicsWithValue(t, http.ErrAbortHandler, func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	})
	assert.Empty(t, th.reported)
}

func TestFormatStackEmpty(t *testing.T) {
	assert.Empty(t, formatStack(nil))
}
