FROM golang:1-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o server .

FROM alpine:3

RUN mkdir /storage
WORKDIR /

COPY --from=builder /app/server /server

EXPOSE 80
VOLUME ["/storage"]

ENTRYPOINT ["/server", "-p=80", "-stderrthreshold=INFO"]
