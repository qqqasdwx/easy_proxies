FROM --platform=$BUILDPLATFORM node:22-slim AS frontend
WORKDIR /frontend
ARG NPM_REGISTRY=https://registry.npmjs.org
RUN npm config set registry ${NPM_REGISTRY}
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ .
RUN npm run build -- --outDir /frontend-dist

FROM --platform=$BUILDPLATFORM golang:1.24 AS builder
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
ARG GOPROXY=https://proxy.golang.org,direct
RUN go env -w GOPROXY=${GOPROXY} && go mod download
COPY . .
RUN rm -rf internal/monitor/assets/*
COPY --from=frontend /frontend-dist/ ./internal/monitor/assets/
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -tags "with_utls with_quic with_grpc with_wireguard with_gvisor with_clash_api" -o easy_proxies ./cmd/easy_proxies

FROM debian:bookworm-slim AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates gosu \
    && rm -rf /var/lib/apt/lists/* \
    && useradd -r -u 10001 easy \
    && mkdir -p /app/data /app/logs /etc/easy_proxies \
    && chown -R easy:easy /app /etc/easy_proxies
WORKDIR /app
COPY --from=builder /src/easy_proxies /usr/local/bin/easy_proxies
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
# Management WebUI/API; proxy listener ports are opened only after nodes are configured.
EXPOSE 9091
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
