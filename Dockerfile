# syntax=docker/dockerfile:1

# ---- web UI
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
# vite writes to ../internal/webui/dist
RUN mkdir -p ../internal/webui/dist && npm run build

# ---- Go binary
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/webui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X github.com/finn-freitag/tossbox/internal/version.Version=${VERSION}" \
      -o /out/tossbox ./cmd/tossbox

# ---- runtime
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata libcap \
 && addgroup -S -g 10001 tossbox && adduser -S -D -H -u 10001 -G tossbox tossbox \
 && mkdir -p /data && chown tossbox:tossbox /data
COPY --from=build /out/tossbox /usr/local/bin/tossbox
# The gateway binds ports 80/443 (and the device port range) as a non-root user.
RUN setcap cap_net_bind_service=+ep /usr/local/bin/tossbox && apk del libcap
USER tossbox
VOLUME /data
ENV TOSSBOX_DATA_DIR=/data
EXPOSE 8080 7443 80 443
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/tossbox"]
