# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/relay-server \
    ./cmd/relay-server

FROM alpine:3.22

RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 relay \
    && adduser -S -D -H -u 10001 -G relay relay \
    && install -d -o relay -g relay /var/lib/relay/artifacts

COPY --from=build /out/relay-server /usr/local/bin/relay-server

USER relay
EXPOSE 8787

HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=5 \
    CMD wget -q -O /dev/null "http://127.0.0.1:${PORT:-8787}/health" || exit 1

ENTRYPOINT ["/usr/local/bin/relay-server"]
