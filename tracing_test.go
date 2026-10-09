package apperror

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// traceUnary runs the unary interceptor inside a recorded span and returns the finished span.
func traceUnary(t *testing.T, handler grpc.UnaryHandler) sdktrace.ReadOnlySpan {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	ctx, span := provider.Tracer("test").Start(context.Background(), "rpc")
	_, _ = newTestHandler().UnaryServerInterceptor()(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/m"}, handler)
	span.End()

	spans := recorder.Ended()
	require.Len(t, spans, 1)
	return spans[0]
}

func returning(err error) grpc.UnaryHandler {
	return func(context.Context, any) (any, error) { return nil, err }
}

func spanAttrs(span sdktrace.ReadOnlySpan) map[attribute.Key]attribute.Value {
	out := map[attribute.Key]attribute.Value{}
	for _, kv := range span.Attributes() {
		out[kv.Key] = kv.Value
	}
	return out
}

func exceptionStack(t *testing.T, span sdktrace.ReadOnlySpan) string {
	t.Helper()
	require.Len(t, span.Events(), 1)
	event := span.Events()[0]
	assert.Equal(t, "exception", event.Name)
	for _, kv := range event.Attributes {
		if kv.Key == "exception.stacktrace" {
			return kv.Value.AsString()
		}
	}
	return ""
}

func TestSpanClientErrorOnlyAttributes(t *testing.T) {
	span := traceUnary(t, returning(NewNotFoundError("USERS", WithCode(4))))

	attrs := spanAttrs(span)
	assert.Equal(t, "NotFound", attrs[attrErrorType].AsString())
	assert.Equal(t, "USERS", attrs[attrErrorSystemCode].AsString())
	assert.Equal(t, int64(4), attrs[attrErrorCode].AsInt64())
	assert.Equal(t, otelcodes.Unset, span.Status().Code, "4xx is not a server error")
	assert.Empty(t, span.Events())
}

func TestSpanInternalErrorRecordedWithStack(t *testing.T) {
	span := traceUnary(t, func(context.Context, any) (any, error) {
		return nil, NewInternalError("USERS", WithErr(errors.New("db down")))
	})

	assert.Equal(t, otelcodes.Error, span.Status().Code)
	assert.Equal(t, "internal error: db down (code: USERS-0)", span.Status().Description)
	assert.Contains(t, exceptionStack(t, span), "TestSpanInternalErrorRecordedWithStack")
}

func TestSpanPanicRecordedWithPanicSiteStack(t *testing.T) {
	span := traceUnary(t, func(context.Context, any) (any, error) { panic("boom") })

	assert.Equal(t, otelcodes.Error, span.Status().Code)
	assert.Equal(t, "InternalSystem", spanAttrs(span)[attrErrorType].AsString())
	assert.Contains(t, exceptionStack(t, span), "TestSpanPanicRecordedWithPanicSiteStack")
}

func TestSpanOtherErrors(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		wantType       string
		wantStatus     otelcodes.Code
		wantSystemCode string
	}{
		{"canceled", context.Canceled, "Canceled", otelcodes.Unset, ""},
		{"deadline", context.DeadlineExceeded, "Timeout", otelcodes.Error, ""},
		{"grpc invalid argument", status.Error(codes.InvalidArgument, "x"), "BadRequest", otelcodes.Unset, ""},
		{"grpc unavailable", status.Error(codes.Unavailable, "x"), "Unavailable", otelcodes.Error, ""},
		{"unknown error", errors.New("boom"), "InternalSystem", otelcodes.Error, "SYS"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			span := traceUnary(t, returning(tt.err))

			attrs := spanAttrs(span)
			assert.Equal(t, tt.wantType, attrs[attrErrorType].AsString())
			assert.Equal(t, tt.wantSystemCode, attrs[attrErrorSystemCode].AsString())
			assert.Equal(t, tt.wantStatus, span.Status().Code)
			assert.Equal(t, tt.wantStatus == otelcodes.Error, len(span.Events()) == 1)
		})
	}
}

func TestSpanHTTP(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	ctx, span := provider.Tracer("test").Start(context.Background(), "http")

	req := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx)
	newTestHandler().WriteHTTPError(httptest.NewRecorder(), req, NewUnavailableError("SYS"))
	span.End()

	ended := recorder.Ended()[0]
	assert.Equal(t, "Unavailable", spanAttrs(ended)[attrErrorType].AsString())
	assert.Equal(t, otelcodes.Error, ended.Status().Code)
}
