package auth

import (
	"context"
	"coscup2025/env"
	"coscup2025/proto/auth"
	"fmt"
	"strings"
	"sync"

	"github.com/golang-jwt/jwt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type authServer struct {
	auth.UnimplementedAuthServiceServer
	users  map[string]user
	mu     sync.RWMutex
	secret []byte
}

func NewAuthServer() *authServer {
	return &authServer{
		users:  make(map[string]user),
		secret: []byte(env.DefaultConfig().JWTSecret),
	}
}

// UnaryInterceptor for JWT validation
func (s *authServer) UnaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	if info.FullMethod == "/auth.AuthService/SignUp" || info.FullMethod == "/auth.AuthService/SignIn" {
		return handler(ctx, req)
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no metadata provided")
	}

	auth, ok := md["authorization"]
	if !ok || len(auth) == 0 {
		return nil, status.Error(codes.Unauthenticated, "authorization token missing")
	}

	tokenString := strings.TrimPrefix(auth[0], "Bearer ")
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil || !token.Valid {
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}

	return handler(ctx, req)
}

// StreamInterceptor for JWT validation on streaming calls
func (s *authServer) StreamInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	// Allow auth service streaming calls (if any) without authentication
	if strings.HasPrefix(info.FullMethod, "/auth.AuthService/") {
		return handler(srv, ss)
	}

	// For media service, require authentication
	ctx := ss.Context()
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "no metadata provided")
	}

	auth, ok := md["authorization"]
	if !ok || len(auth) == 0 {
		return status.Error(codes.Unauthenticated, "authorization token missing")
	}

	tokenString := strings.TrimPrefix(auth[0], "Bearer ")
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil || !token.Valid {
		return status.Error(codes.Unauthenticated, "invalid token")
	}

	// Add user info to context for downstream use
	if claims, ok := token.Claims.(jwt.MapClaims); ok {
		if username, exists := claims["username"]; exists {
			ctx = metadata.AppendToOutgoingContext(ctx, "x-user-id", username.(string))
			ss = &ServerCtxStream{ServerStream: ss, ctx: ctx}
		}
	}

	return handler(srv, ss)
}

// ServerCtxStream wraps grpc.ServerStream to override Context()
type ServerCtxStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *ServerCtxStream) Context() context.Context {
	return s.ctx
}
