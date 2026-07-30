# syntax=docker/dockerfile:1

FROM node:22.12.0-bookworm-slim AS frontend-build

WORKDIR /src

COPY pnpm-workspace.yaml pnpm-lock.yaml ./
COPY web/package.json web/package.json
RUN npm install --global pnpm@10.28.2 \
    && pnpm install --frozen-lockfile

COPY web web
RUN pnpm --dir web run build \
    && if [ -d internal/web/dist ]; then \
         :; \
       elif [ -d web/dist ]; then \
         mkdir -p internal/web/dist \
         && cp -a web/dist/. internal/web/dist/; \
       else \
         echo "frontend build did not produce a dist directory" >&2; \
         exit 1; \
       fi

FROM golang:1.24.0-bookworm AS go-build

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOTOOLCHAIN=local

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=frontend-build /src/internal/web/dist ./internal/web/dist
RUN go build -trimpath -ldflags="-s -w" -o /out/nvidia-router ./cmd/nvidia-router

FROM debian:bookworm-slim AS runtime

RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 nvidia-router \
    && useradd --uid 10001 --gid 10001 --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin nvidia-router \
    && install -d -o 10001 -g 10001 -m 0750 /data \
    && chmod 1777 /tmp

COPY --from=go-build --chown=10001:10001 /out/nvidia-router /usr/local/bin/nvidia-router

ENV NVIDIA_ROUTER_LISTEN_ADDR=0.0.0.0:3756 \
    NVIDIA_ROUTER_DATA_DIR=/data \
    NVIDIA_ROUTER_TEMP_DIR=/tmp

EXPOSE 3756
VOLUME ["/data"]
USER 10001:10001

ENTRYPOINT ["/usr/local/bin/nvidia-router"]
CMD ["serve"]
