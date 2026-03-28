FROM golang:1.22-alpine AS builder

RUN addgroup -S -g 1001 notty && \
    adduser -S -u 1001 -G notty notty

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o notty ./cmd/notty && \
    mkdir -p data

FROM busybox:1.36.1-musl AS shell

FROM scratch

COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Fly console helper commands require /bin/sh and /bin/sleep.
COPY --from=shell /bin/busybox /bin/sh
COPY --from=shell /bin/busybox /bin/sleep

COPY --from=builder --chown=1001:1001 /build/notty /app/notty
COPY --from=builder --chown=1001:1001 /build/web /app/web
COPY --from=builder --chown=1001:1001 /build/data /data

WORKDIR /app

USER 1001

EXPOSE 8080

ENTRYPOINT ["/app/notty"]
