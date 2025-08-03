package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"coscup2025/proto/media"
)

const (
	chunkSize = 1024 * 1024 // 1MB chunks
)

func main() {
	if len(os.Args) != 4 {
		log.Fatal("Usage: go run main.go <jwt_token> <video_id> <video_file_path>")
	}

	token := os.Args[1]
	videoID := os.Args[2]
	videoFilePath := os.Args[3]

	conn, err := grpc.Dial("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to server: %v", err)
	}
	defer conn.Close()

	client := media.NewMediaServiceClient(conn)

	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))

	err = uploadVideo(client, videoID, videoFilePath, ctx)
	if err != nil {
		log.Fatalf("Failed to upload video: %v", err)
	}

	fmt.Printf("Successfully uploaded video: %s\n", videoID)
}

func uploadVideo(client media.MediaServiceClient, videoID, filePath string, ctx context.Context) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %v", err)
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info: %v", err)
	}

	fmt.Printf("Uploading video: %s (size: %d bytes)\n", videoID, fileInfo.Size())

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	stream, err := client.UploadVideo(ctx)
	if err != nil {
		return fmt.Errorf("failed to create upload stream: %v", err)
	}

	buffer := make([]byte, chunkSize)
	sequence := int64(1)
	totalBytes := int64(0)

	for {
		n, err := file.Read(buffer)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read file: %v", err)
		}

		chunk := &media.UploadVideoRequest{
			VideoId:  videoID,
			Data:     buffer[:n],
			Sequence: sequence,
		}

		err = stream.Send(chunk)
		if err != nil {
			return fmt.Errorf("failed to send chunk %d: %v", sequence, err)
		}

		totalBytes += int64(n)
		sequence++

		fmt.Printf("Sent chunk %d: %d bytes (total: %d bytes)\n", sequence-1, n, totalBytes)
	}

	response, err := stream.CloseAndRecv()
	if err != nil {
		return fmt.Errorf("failed to close stream: %v", err)
	}

	fmt.Printf("Upload completed: %s, %d bytes\n", response.VideoId, response.TotalBytes)
	return nil
}
