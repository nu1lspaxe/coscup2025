# COSCUP 2025 DEMO 


Some useful manual scripts : 

```bash
curl -X POST http://localhost:8080/v1/signup -H "Content-Type: application/json" -d '{"username": "testuser", "password": "testpass"}'

curl -X POST http://localhost:8080/v1/signin  -H "Content-Type: application/json"  -d '{"username": "testuser", "password": "testpass"}'

curl -X GET http://localhost:8080/v1/profile -H "Authorization: Bearer <jwt_token>"

# media/client/upload
go run main.go <jwt_token> video_1280x720_1mb ../video_1280x720_1mb.mp4

# media/client/download
go run main.go <jwt_token> video_1280x720_1mb ./video_1280x720_1mb.mp4
```

## Enable OpenTelemetry

```bash
docker compose up -d

# Then go to http://127.0.0.1:16686/search
```