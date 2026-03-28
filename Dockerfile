FROM golang:1.22-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o notty ./cmd/notty


FROM alpine:3.19

RUN apk add --no-cache sqlite ca-certificates tzdata


RUN addgroup -S -g 1001 notty && \
    adduser -S -u 1001 -G notty notty

WORKDIR /app


COPY --from=builder --chown=1001:1001 /build/notty /app/notty
COPY --from=builder --chown=1001:1001 /build/web /app/web


RUN mkdir -p /data && chown -R 1001:1001 /data

USER 1001

EXPOSE 8080
ENTRYPOINT ["/app/notty"]
