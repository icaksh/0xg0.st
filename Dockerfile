FROM golang:1-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o server .

FROM alpine:3

RUN mkdir -p /storage && chmod 0777 /storage
WORKDIR /app

COPY --from=builder /app/server /app/server
COPY --from=builder /app/templates /app/templates

EXPOSE 80
VOLUME ["/storage"]

ENTRYPOINT ["/app/server", "-p=80", "-stderrthreshold=INFO"]
