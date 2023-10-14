FROM golang:1.16 as builder

WORKDIR /app

COPY go.* ./
RUN go mod download

COPY . ./

RUN CGO_ENABLED=0 GOOS=linux go build -v -o server

FROM gcr.io/distroless/base-debian10
COPY --from=builder /app/server /server
CMD ["/server"]
