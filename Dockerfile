FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod ./
COPY main.go ./
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build -o /token-gateway .

FROM alpine:3.22

WORKDIR /app

COPY --from=builder /token-gateway /usr/local/bin/token-gateway

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/token-gateway"]
