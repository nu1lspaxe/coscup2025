package auth

import (
	"context"
	"coscup2025/proto/auth"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

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
		"iat":     time.Now().Unix(),
		"exp":     time.Now().Add(time.Hour * 24).Unix(),
	})
	tokenString, err := token.SignedString(s.secret)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate token")
	}

	if err := grpc.SetHeader(ctx, metadata.Pairs("x-auth-token", tokenString)); err != nil {
		return nil, err
	}

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
