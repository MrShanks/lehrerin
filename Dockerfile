FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/lehrerin ./cmd/lehrerin

FROM alpine:3.20
RUN adduser -D -H -u 10001 lehrerin
WORKDIR /app
COPY --from=builder /out/lehrerin ./lehrerin
RUN mkdir -p /app/data && chown -R lehrerin:lehrerin /app
USER lehrerin
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["./lehrerin"]
