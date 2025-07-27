package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/golang-jwt/jwt"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pb "proto/auth"
)

// In-memory user store for demo purposes
type user struct {
	ID       string
	Username string
	Password string
}

type authServer struct {
	pb.UnimplementedAuthServiceServer
	users  map[string]user
	mu     sync.RWMutex
	secret []byte
}

func NewAuthServer() *authServer {
	return &authServer{
		users:  make(map[string]user),
		secret: []byte("my-secret-key"),
	}
}

func (s *authServer) SignUp(ctx context.Context, req *pb.SignUpRequest) (*pb.SignUpResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Simple validation
	if req.Username == "" || req.Password == "" {
		return nil, status.Error(codes.InvalidArgument, "username and password are required")
	}

	userID := fmt.Sprintf("user_%d", len(s.users)+1)
	s.users[req.Username] = user{
		ID:       userID,
		Username: req.Username,
		Password: req.Password, // In production, hash the password
	}

	return &pb.SignUpResponse{UserId: userID}, nil
}

func (s *authServer) SignIn(ctx context.Context, req *pb.SignInRequest) (*pb.SignInResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, exists := s.users[req.Username]
	if !exists || user.Password != req.Password {
		return nil, status.Error(codes.Unauthenticated, "invalid credentials")
	}

	// Generate JWT token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": user.ID,
		"sub":     user.Username,
	})
	tokenString, err := token.SignedString(s.secret)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate token")
	}

	// Set response metadata
	grpc.SetHeader(ctx, metadata.Pairs("x-auth-token", tokenString))

	return &pb.SignInResponse{Token: tokenString}, nil
}

// UnaryInterceptor for JWT validation
func (s *authServer) UnaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	// Skip authentication for SignUp and SignIn
	if info.FullMethod == "/auth.AuthService/SignUp" || info.FullMethod == "/auth.AuthService/SignIn" {
		return handler(ctx, req)
	}

	// Extract metadata
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no metadata provided")
	}

	// Check for authorization header
	auth, ok := md["authorization"]
	if !ok || len(auth) == 0 {
		return nil, status.Error(codes.Unauthenticated, "authorization token missing")
	}

	// Validate JWT token
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

func main() {
	// Start gRPC server
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	server := grpc.NewServer(
		grpc.UnaryInterceptor(NewAuthServer().UnaryInterceptor),
	)
	pb.RegisterAuthServiceServer(server, NewAuthServer())

	go func() {
		log.Printf("gRPC server listening at %v", lis.Addr())
		if err := server.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()

	// Start gRPC-Gateway
	ctx := context.Background()
	mux := runtime.NewServeMux(
		runtime.WithIncomingHeaderMatcher(func(key string) (string, bool) {
			switch strings.ToLower(key) {
			case "authorization":
				return "authorization", true
			default:
				return runtime.DefaultHeaderMatcher(key)
			}
		}),
		runtime.WithForwardResponseOption(func(ctx context.Context, w http.ResponseWriter, _ proto.Message) error {
			md, ok := runtime.ServerMetadataFromContext(ctx)
			if ok {
				if tokens := md.HeaderMD.Get("x-auth-token"); len(tokens) > 0 {
					w.Header().Set("X-Auth-Token", tokens[0])
				}
			}
			return nil
		}),
	)

	// Register gRPC-Gateway with the gRPC server
	dialOpts := []grpc.DialOption{grpc.WithInsecure()} // Use WithTransportCredentials for TLS in production
	err = pb.RegisterAuthServiceHandlerFromEndpoint(ctx, mux, "localhost:50051", dialOpts)
	if err != nil {
		log.Fatalf("failed to register gateway: %v", err)
	}

	// Start HTTP server for gRPC-Gateway
	log.Printf("gRPC-Gateway listening at :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatalf("failed to serve gateway: %v", err)
	}
}
