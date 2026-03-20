# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

# Build all three binaries — stripped and statically linked.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o xiphos-worker ./cmd/xiphos-worker
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o xiphos-collector ./cmd/xiphos-collector
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o xiphos-ingestor ./cmd/xiphos-ingestor

# Worker image — includes git for cloning repos.
FROM alpine:3.19 AS worker

RUN apk --no-cache add ca-certificates git

WORKDIR /root/
COPY --from=builder /app/xiphos-worker .
ENTRYPOINT ["./xiphos-worker"]

# Collector image — no git needed.
FROM alpine:3.19 AS collector

RUN apk --no-cache add ca-certificates

WORKDIR /root/
COPY --from=builder /app/xiphos-collector .
ENTRYPOINT ["./xiphos-collector"]

# Ingestor image — no git needed.
FROM alpine:3.19 AS ingestor

RUN apk --no-cache add ca-certificates

WORKDIR /root/
COPY --from=builder /app/xiphos-ingestor .
ENTRYPOINT ["./xiphos-ingestor"]
