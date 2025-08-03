package media

import (
	"coscup2025/proto/media"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

type VideoInfo struct {
	Data     []byte
	Metadata *media.VideoMetadata
}

type mediaServer struct {
	media.UnimplementedMediaServiceServer
	videos map[string]*VideoInfo
	mu     sync.RWMutex
	tracer trace.Tracer
}

func NewMediaServer() *mediaServer {
	return &mediaServer{
		videos: make(map[string]*VideoInfo),
		tracer: otel.Tracer("media-service"),
	}
}
