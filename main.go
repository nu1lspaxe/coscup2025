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
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"coscup2025/proto/auth"
)

type user struct {
	ID       string
	Username string
	Password string
}

type authServer struct {
	auth.UnimplementedAuthServiceServer
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

func (s *authServer) SignUp(ctx context.Context, req *auth.SignUpRequest) (*auth.SignUpResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.Username == "" || req.Password == "" {
		return nil, status.Error(codes.InvalidArgument, "username and password are required")
	}

	bcryptPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to hash password")
	}

	userID := fmt.Sprintf("user_%d", len(s.users)+1)
	s.users[req.Username] = user{
		ID:       userID,
		Username: req.Username,
		Password: string(bcryptPassword),
	}

	return &auth.SignUpResponse{UserId: userID}, nil
}

func (s *authServer) SignIn(ctx context.Context, req *auth.SignInRequest) (*auth.SignInResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, exists := s.users[req.Username]
	if !exists || bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)) != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid credentials")
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": user.ID,
		"sub":     user.Username,
	})
	tokenString, err := token.SignedString(s.secret)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate token")
	}

	grpc.SetHeader(ctx, metadata.Pairs("x-auth-token", tokenString))

	return &auth.SignInResponse{Token: tokenString}, nil
}

func (s *authServer) GetUserProfile(ctx context.Context, req *auth.GetUserProfileRequest) (*auth.GetUserProfileResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Extract user_id from JWT claims
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no metadata provided")
	}

	authToken, ok := md["authorization"]
	if !ok || len(authToken) == 0 {
		return nil, status.Error(codes.Unauthenticated, "authorization token missing")
	}

	tokenString := strings.TrimPrefix(authToken[0], "Bearer ")
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil || !token.Valid {
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "invalid token claims")
	}

	userID, ok := claims["user_id"].(string)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "invalid user_id in token")
	}

	username, ok := claims["sub"].(string)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "invalid username in token")
	}

	user, exists := s.users[username]
	if !exists || user.ID != userID {
		return nil, status.Error(codes.Unauthenticated, "user not found")
	}

	return &auth.GetUserProfileResponse{
		UserId:   userID,
		Username: username,
	}, nil
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

func main() {
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	authSrv := NewAuthServer()
	server := grpc.NewServer(
		grpc.UnaryInterceptor(authSrv.UnaryInterceptor),
	)
	auth.RegisterAuthServiceServer(server, authSrv)

	go func() {
		log.Printf("gRPC server listening at %v", lis.Addr())
		if err := server.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()

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

	dialOpts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	err = auth.RegisterAuthServiceHandlerFromEndpoint(ctx, mux, "localhost:50051", dialOpts)
	if err != nil {
		log.Fatalf("failed to register gateway: %v", err)
	}

	log.Printf("gRPC-Gateway listening at :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatalf("failed to serve gateway: %v", err)
	}
}
