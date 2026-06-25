# syntax=docker/dockerfile:1

# ---- build stage ----
FROM golang:1.26-alpine AS builder

WORKDIR /src

# 의존성 캐시 레이어
COPY go.mod go.sum ./
RUN go mod download

# 소스 복사 후 정적 바이너리 빌드 (CGO 비활성 -> scratch에서 실행 가능)
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/server ./cmd/server

# ---- runtime stage ----
FROM alpine:3.20

# OpenAI/pgx TLS 통신을 위한 루트 인증서
RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY --from=builder /app/server /app/server

EXPOSE 8080
ENTRYPOINT ["/app/server"]
