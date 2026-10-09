package apperror

import (
	"context"

	"google.golang.org/grpc"
)

// UnaryServerInterceptor returns a gRPC unary interceptor applying the handler's error policy.
func (h *ErrorHandler) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (resp any, err error) {
		defer h.recoverGRPC(ctx, info.FullMethod, req, &err)

		resp, err = handler(ctx, req)
		if err == nil {
			return resp, nil
		}
		return nil, h.handle(ctx, info.FullMethod, req, err)
	}
}

// StreamServerInterceptor returns a gRPC stream interceptor applying the handler's error policy.
func (h *ErrorHandler) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer h.recoverGRPC(ss.Context(), info.FullMethod, nil, &err)

		err = handler(srv, ss)
		if err == nil {
			return nil
		}
		return h.handle(ss.Context(), info.FullMethod, nil, err)
	}
}

// GRPCUnaryInterceptor is a shorthand for NewErrorHandler(systemCode, opts...).UnaryServerInterceptor().
func GRPCUnaryInterceptor(systemCode string, opts ...HandlerOption) grpc.UnaryServerInterceptor {
	return NewErrorHandler(systemCode, opts...).UnaryServerInterceptor()
}

// GRPCStreamInterceptor is a shorthand for NewErrorHandler(systemCode, opts...).StreamServerInterceptor().
func GRPCStreamInterceptor(systemCode string, opts ...HandlerOption) grpc.StreamServerInterceptor {
	return NewErrorHandler(systemCode, opts...).StreamServerInterceptor()
}
