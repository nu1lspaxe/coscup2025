package main

import (
	"context"
	"coscup2025/proto/media"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

func main() {
	if len(os.Args) != 4 {
		log.Fatal("Usage: go run main.go <jwt_token> <video_id> <output_file_path>")
	}

	token := os.Args[1]
	videoID := os.Args[2]
	outputFilePath := os.Args[3]

	conn, err := grpc.NewClient("localhost:50051", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to server: %v", err)
	}
	defer conn.Close()

	client := media.NewMediaServiceClient(conn)

	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))

	err = downloadVideo(client, videoID, outputFilePath, ctx)
	if err != nil {
		log.Fatalf("Failed to download video: %v", err)
	}

	fmt.Printf("Successfully downloaded video: %s to %s\n", videoID, outputFilePath)
}

func downloadVideo(client media.MediaServiceClient, videoID, outputPath string, ctx context.Context) error {
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %v", err)
	}
	defer file.Close()

	fmt.Printf("Downloading video: %s\n", videoID)

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	stream, err := client.DownloadVideo(ctx, &media.DownloadVideoRequest{
		VideoId: videoID,
	})
	if err != nil {
		return fmt.Errorf("failed to create download stream: %v", err)
	}

	totalBytes := int64(0)
	chunkCount := int64(0)
	var videoMetadata *media.VideoMetadata

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to receive chunk: %v", err)
		}

		if chunk.Sequence == 1 && chunk.Metadata != nil {
			videoMetadata = chunk.Metadata
			fmt.Println("\n=== Metadata ====")
			fmt.Printf("Uploader ID: %s\n", videoMetadata.UploaderId)
			fmt.Printf("Uploader Name: %s\n", videoMetadata.UploaderName)
			fmt.Printf("File Name: %s\n", videoMetadata.FileName)
			fmt.Printf("File Size: %d bytes\n", videoMetadata.FileSize)
			if videoMetadata.UploadTimestamp > 0 {
				uploadTime := time.Unix(videoMetadata.UploadTimestamp, 0)
				fmt.Printf("Upload Time: %s\n", uploadTime.Format("2006-01-02 15:04:05"))
			}
		}

		n, err := file.Write(chunk.Data)
		if err != nil {
			return fmt.Errorf("failed to write chunk to file: %v", err)
		}

		totalBytes += int64(n)
		chunkCount++

		fmt.Printf("Received chunk %d: %d bytes (total: %d bytes)\n", chunk.Sequence, n, totalBytes)
	}

	fmt.Printf("Download completed: %d bytes in %d chunks\n", totalBytes, chunkCount)

	if videoMetadata != nil {
		fmt.Println("\n=== Download Summary ====")
		fmt.Printf("Uploader Name: %s (%s)\n", videoMetadata.UploaderName, videoMetadata.UploaderId)
		if videoMetadata.UploadTimestamp > 0 {
			uploadTime := time.Unix(videoMetadata.UploadTimestamp, 0)
			fmt.Printf("Upload Time: %s\n", uploadTime.Format("2006-01-02 15:04:05"))
		}
		fmt.Printf("Output Path: %s\n", outputPath)
		fmt.Println("===================")
	}

	return nil
}
