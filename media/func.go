package media

import (
	"coscup2025/proto/media"
	"io"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func (s *mediaServer) UploadVideo(stream media.MediaService_UploadVideoServer) error {
	_, span := s.tracer.Start(stream.Context(), "UploadVideo")
	defer span.End()

	var videoID string
	var totalBytes int64
	var videoData []byte
	var chunkCount int64

	span.SetAttributes(
		attribute.String("service.name", "media-service"),
		attribute.String("rpc.method", "UploadVideo"),
		attribute.String("rpc.service", "MediaService"),
	)

	for {
		req, err := stream.Recv()
		if err == io.EOF {
			if videoID == "" {
				err := status.Error(grpccodes.InvalidArgument, "no video ID provided")
				span.RecordError(err)
				span.SetStatus(codes.Error, "no video ID provided")
				return err
			}

			md, _ := metadata.FromIncomingContext(stream.Context())
			uploaderID := "unknown"
			uploaderName := "Unknown User"

			if userIDs := md.Get("user-id"); len(userIDs) > 0 {
				uploaderID = userIDs[0]
			}
			if userNames := md.Get("user-name"); len(userNames) > 0 {
				uploaderName = userNames[0]
			}

			metadata := &media.VideoMetadata{
				UploaderId:      uploaderID,
				UploaderName:    uploaderName,
				UploadTimestamp: time.Now().Unix(),
				FileName:        videoID,
				FileSize:        totalBytes,
			}

			s.mu.Lock()
			s.videos[videoID] = &VideoInfo{
				Data:     videoData,
				Metadata: metadata,
			}
			s.mu.Unlock()

			span.SetAttributes(
				attribute.String("video.id", videoID),
				attribute.Int64("video.size_bytes", totalBytes),
				attribute.Int64("video.chunk_count", chunkCount),
				attribute.String("operation.status", "success"),
			)

			span.AddEvent("video_upload_completed", trace.WithAttributes(
				attribute.String("video.id", videoID),
				attribute.Int64("total_bytes", totalBytes),
			))

			span.SetStatus(codes.Ok, "upload completed successfully")

			return stream.SendAndClose(&media.UploadVideoResponse{
				VideoId:    videoID,
				TotalBytes: totalBytes,
				Metadata:   metadata,
			})
		}
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to receive chunk")
			span.SetAttributes(attribute.String("error.type", "stream_receive_error"))
			return status.Errorf(grpccodes.Internal, "failed to receive chunk: %v", err)
		}

		if videoID == "" {
			if req.VideoId == "" {
				err := status.Error(grpccodes.InvalidArgument, "video ID is required")
				span.RecordError(err)
				span.SetStatus(codes.Error, "video ID is required")
				return err
			}
			videoID = req.VideoId
			span.SetAttributes(
				attribute.String("video.id", videoID),
				attribute.String("operation.phase", "receiving_chunks"),
			)
			span.AddEvent("video_upload_started", trace.WithAttributes(
				attribute.String("video.id", videoID),
			))
		}

		if req.VideoId != videoID {
			err := status.Error(grpccodes.InvalidArgument, "inconsistent video ID")
			span.RecordError(err)
			span.SetStatus(codes.Error, "inconsistent video ID")
			span.SetAttributes(
				attribute.String("error.type", "inconsistent_video_id"),
				attribute.String("expected_video_id", videoID),
				attribute.String("received_video_id", req.VideoId),
			)
			return err
		}

		videoData = append(videoData, req.Data...)
		totalBytes += int64(len(req.Data))
		chunkCount++

		span.AddEvent("chunk_received", trace.WithAttributes(
			attribute.Int64("chunk.size_bytes", int64(len(req.Data))),
			attribute.Int64("chunk.sequence", req.Sequence),
			attribute.Int64("chunk.number", chunkCount),
			attribute.Int64("total_bytes_received", totalBytes),
		))
	}
}

func (s *mediaServer) DownloadVideo(req *media.DownloadVideoRequest, stream media.MediaService_DownloadVideoServer) error {
	_, span := s.tracer.Start(stream.Context(), "DownloadVideo")
	defer span.End()

	span.SetAttributes(
		attribute.String("service.name", "media-service"),
		attribute.String("rpc.method", "DownloadVideo"),
		attribute.String("rpc.service", "MediaService"),
		attribute.String("video.id", req.VideoId),
	)

	span.AddEvent("video_download_started", trace.WithAttributes(
		attribute.String("video.id", req.VideoId),
	))

	s.mu.RLock()
	videoInfo, exists := s.videos[req.VideoId]
	s.mu.RUnlock()

	if !exists {
		err := status.Error(grpccodes.NotFound, "video not found")
		span.RecordError(err)
		span.SetStatus(codes.Error, "video not found")
		span.SetAttributes(
			attribute.String("error.type", "video_not_found"),
			attribute.String("requested_video_id", req.VideoId),
		)
		return err
	}

	if len(videoInfo.Data) == 0 {
		err := status.Error(grpccodes.FailedPrecondition, "no download source available for this video")
		span.RecordError(err)
		span.SetStatus(codes.Error, "no download source available")
		span.SetAttributes(
			attribute.String("error.type", "no_download_source"),
			attribute.String("video.id", req.VideoId),
		)
		return err
	}

	videoData := videoInfo.Data
	videoSize := int64(len(videoData))
	chunkSize := 1024 * 1024
	totalChunks := int64((len(videoData) + chunkSize - 1) / chunkSize)

	span.SetAttributes(
		attribute.Int64("video.size_bytes", videoSize),
		attribute.Int64("video.chunk_size", int64(chunkSize)),
		attribute.Int64("video.total_chunks", totalChunks),
		attribute.String("operation.phase", "sending_chunks"),
	)

	var chunksSent int64
	for i := 0; i < len(videoData); i += chunkSize {
		end := i + chunkSize
		if end > len(videoData) {
			end = len(videoData)
		}

		chunkSequence := int64(i/chunkSize + 1)

		// Create response with metadata only in first chunk
		response := &media.DownloadVideoResponse{
			VideoId:  req.VideoId,
			Data:     videoData[i:end],
			Sequence: chunkSequence,
		}

		// Include metadata only in the first chunk
		if chunkSequence == 1 {
			response.Metadata = videoInfo.Metadata
		}

		err := stream.Send(response)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to send chunk")
			span.SetAttributes(
				attribute.String("error.type", "stream_send_error"),
				attribute.Int64("failed_chunk_sequence", chunkSequence),
			)
			return status.Errorf(grpccodes.Internal, "failed to send chunk: %v", err)
		}

		chunksSent++
		span.AddEvent("chunk_sent", trace.WithAttributes(
			attribute.Int64("chunk.size_bytes", int64(end-i)),
			attribute.Int64("chunk.sequence", chunkSequence),
			attribute.Int64("chunks_sent", chunksSent),
			attribute.Int64("bytes_sent", int64(end)),
		))
	}

	span.AddEvent("video_download_completed", trace.WithAttributes(
		attribute.String("video.id", req.VideoId),
		attribute.Int64("total_bytes_sent", videoSize),
		attribute.Int64("total_chunks_sent", chunksSent),
	))

	span.SetAttributes(
		attribute.String("operation.status", "success"),
		attribute.Int64("final_chunks_sent", chunksSent),
	)

	span.SetStatus(codes.Ok, "download completed successfully")

	return nil
}
