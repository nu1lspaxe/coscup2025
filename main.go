package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	"coscup2025/auth"
	"coscup2025/media"

	pbAuth "coscup2025/proto/auth"
	pbMedia "coscup2025/proto/media"
)

func initTracer() func() {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	exporter, err := otlptracegrpc.New(context.Background(),
		otlptracegrpc.WithEndpoint("localhost:4317"),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		log.Printf("Failed to create OTLP exporter: %v", err)
		return func() {}
	}

	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceName("coscup2025-service"),
			semconv.ServiceVersion("1.0.0"),
		),
	)
	if err != nil {
		log.Printf("Failed to create resource: %v", err)
		return func() {}
	}

	tp := trace.NewTracerProvider(
		trace.WithBatcher(exporter),
		trace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			log.Printf("Error shutting down tracer provider: %v", err)
		}
	}
}

func main() {
	cleanup := initTracer()
	defer cleanup()

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	authSrv := auth.NewAuthServer()
	mediaSrv := media.NewMediaServer()
	server := grpc.NewServer(
		grpc.UnaryInterceptor(authSrv.UnaryInterceptor),
		grpc.StreamInterceptor(authSrv.StreamInterceptor),
	)
	pbAuth.RegisterAuthServiceServer(server, authSrv)
	pbMedia.RegisterMediaServiceServer(server, mediaSrv)

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

	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	err = pbAuth.RegisterAuthServiceHandlerFromEndpoint(ctx, mux, "localhost:50051", dialOpts)
	if err != nil {
		log.Fatalf("failed to register gateway: %v", err)
	}
	err = pbMedia.RegisterMediaServiceHandlerFromEndpoint(ctx, mux, "localhost:50051", dialOpts)
	if err != nil {
		log.Fatalf("failed to register gateway: %v", err)
	}

	log.Printf("gRPC-Gateway listening at :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatalf("failed to serve gateway: %v", err)
	}
}
