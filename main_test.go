package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang-jwt/jwt"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pbAuth "coscup2025/proto/auth"

	"coscup2025/auth"
)

func setupTestServer(t *testing.T) (*grpc.Server, *runtime.ServeMux, *bufconn.Listener) {
	lis := bufconn.Listen(1024 * 1024)

	authSrv := auth.NewAuthServer()
	server := grpc.NewServer(
		grpc.UnaryInterceptor(authSrv.UnaryInterceptor),
	)
	pbAuth.RegisterAuthServiceServer(server, authSrv)

	go func() {
		if err := server.Serve(lis); err != nil {
			t.Errorf("failed to serve: %v", err)
		}
	}()

	mux := runtime.NewServeMux(
		runtime.WithIncomingHeaderMatcher(func(key string) (string, bool) {
			switch key {
			case "Authorization":
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

	ctx := context.Background()
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
	}
	err := pbAuth.RegisterAuthServiceHandlerFromEndpoint(ctx, mux, "localhost:50051", dialOpts)
	if err != nil {
		t.Fatalf("failed to register gateway: %v", err)
	}

	return server, mux, lis
}

func TestGetUserProfileHeaderAuthentication(t *testing.T) {
	server, mux, lis := setupTestServer(t)
	defer server.Stop()
	defer lis.Close()

	makeRequest := func(t *testing.T, token string) (*http.Response, *pbAuth.GetUserProfileResponse) {
		req, err := http.NewRequest("GET", "/v1/profile", nil)
		require.NoError(t, err, "Failed to create request")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		resp := rr.Result()

		var profile pbAuth.GetUserProfileResponse
		if resp.StatusCode == http.StatusOK {
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err, "Failed to read response body")
			t.Logf("GetUserProfile response body: %s", string(body))
			err = protojson.Unmarshal(body, &profile)
			require.NoError(t, err, "Failed to decode response body")
		}
		return resp, &profile
	}

	// Step 1: Sign up a user
	signUpReq := &pbAuth.SignUpRequest{Username: "testuser", Password: "testpass"}
	signUpJSON, err := json.Marshal(signUpReq)
	require.NoError(t, err, "Failed to marshal SignUp request")
	req, err := http.NewRequest("POST", "/v1/signup", bytes.NewBuffer(signUpJSON))
	require.NoError(t, err, "Failed to create SignUp request")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "SignUp failed")

	// Step 2: Sign in to get a JWT token
	signInReq := &pbAuth.SignInRequest{Username: "testuser", Password: "testpass"}
	signInJSON, err := json.Marshal(signInReq)
	require.NoError(t, err, "Failed to marshal SignIn request")
	req, err = http.NewRequest("POST", "/v1/signin", bytes.NewBuffer(signInJSON))
	require.NoError(t, err, "Failed to create SignIn request")
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "SignIn failed")

	var signInResp pbAuth.SignInResponse
	err = json.NewDecoder(rr.Body).Decode(&signInResp)
	require.NoError(t, err, "Failed to decode SignIn response")
	token := signInResp.Token
	require.NotEmpty(t, token, "No token returned from SignIn")

	// Debug: Parse and log JWT token claims
	parsedToken, _, err := new(jwt.Parser).ParseUnverified(token, jwt.MapClaims{})
	require.NoError(t, err, "Failed to parse JWT token")
	claims, ok := parsedToken.Claims.(jwt.MapClaims)
	require.True(t, ok, "Invalid JWT claims")
	t.Logf("JWT claims: %+v", claims)

	// Step 3: Test GetUserProfile with valid token
	resp, profile := makeRequest(t, token)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "Expected status 200")
	assert.NotEmpty(t, profile.UserId, "Expected non-empty user_id")
	assert.Equal(t, "testuser", profile.Username, "Expected username 'testuser'")

	// Step 4: Test GetUserProfile with no token
	resp, _ = makeRequest(t, "")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "Expected status 401 for missing token")
}
