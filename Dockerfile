
# syntax=docker/dockerfile:1

# Intermediate build container
FROM golang:1.21 AS builder

# Set destination for COPY
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./

RUN CGO_ENABLED=0 GOOS=linux go build -o /app/docker-gs-ping

# Final production container
FROM alpine:latest

# Copy built binary from builder container
COPY --from=builder /app/docker-gs-ping /docker-gs-ping

EXPOSE 8080

CMD ["/docker-gs-ping"]

