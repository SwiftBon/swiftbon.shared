package principal

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// UnaryServerInterceptor is Require for gRPC: the same verification and the
// same requirements, with the token read from the x-swiftbon-principal
// metadata key. A missing or invalid token is codes.Unauthenticated; an unmet
// requirement is codes.PermissionDenied.
func UnaryServerInterceptor(v Verifier, opts ...Option) grpc.UnaryServerInterceptor {
	req := newRequirements(opts)
	return func(ctx context.Context, in any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		if req.skipMethods[info.FullMethod] {
			return next(ctx, in)
		}
		var token string
		// md.Get returns an empty slice for an absent key, so the length is
		// checked before indexing: a caller that omits the header must get
		// Unauthenticated, not a panic.
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if vals := md.Get(MetadataKey); len(vals) > 0 {
				token = vals[0]
			}
		}
		p, err := v.Verify(token)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, CodeUnauthenticated)
		}
		if err := req.check(p); err != nil {
			return nil, status.Error(codes.PermissionDenied, CodeForbidden+": "+reasonOf(err))
		}
		return next(WithContext(ctx, p), in)
	}
}

// OutgoingContext returns ctx with the token attached as outgoing gRPC
// metadata, for a service calling another on a user's behalf.
func OutgoingContext(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, MetadataKey, token)
}
