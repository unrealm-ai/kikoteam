FROM --platform=$BUILDPLATFORM golang:1.27.2-bookworm AS go-build
ARG TARGETOS
ARG TARGETARCH
ENV CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH
WORKDIR /src/agent-engine
COPY agent-engine/go.mod agent-engine/go.sum ./
RUN go mod download
COPY agent-engine/cmd ./cmd
COPY agent-engine/internal ./internal
RUN go build -trimpath -o /out/agent-engine ./cmd/agent-engine
WORKDIR /src/sandbox-runner
COPY sandbox-runner/go.mod ./
COPY sandbox-runner/cmd ./cmd
COPY sandbox-runner/internal ./internal
RUN go build -trimpath -o /out/sandbox-runner ./cmd/sandbox-runner

FROM node:24.18.1-trixie-slim AS node-build
RUN apt-get update && apt-get install -y --no-install-recommends build-essential python3 \
    && rm -rf /var/lib/apt/lists/*
ENV npm_config_nodedir=/usr/local
WORKDIR /src/agent-engine
COPY agent-engine/package*.json ./
RUN npm ci --omit=dev --no-audit --no-fund
WORKDIR /src/builtin-mcp
COPY sandbox-runner/builtin-mcp/package*.json ./
RUN npm ci --no-audit --no-fund && npm rebuild node-pty
COPY sandbox-runner/builtin-mcp/ ./
RUN npm run build && npm prune --omit=dev --no-audit --no-fund

FROM node:24.18.1-trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash ca-certificates fd-find procps python3 ripgrep \
    python3-openpyxl python3-pandas python3-lxml python3-pil python3-venv \
    libreoffice-calc libreoffice-impress poppler-utils fonts-noto-cjk \
    && ln -s /usr/bin/fdfind /usr/local/bin/fd \
    && rm -rf /var/lib/apt/lists/*
RUN python3 -m venv --system-site-packages /opt/office && \
    /opt/office/bin/pip install --no-cache-dir 'markitdown[pptx]==0.1.4'
ENV PATH=/opt/office/bin:$PATH \
    NODE_PATH=/opt/kikoteam/agent-engine/node_modules \
    KIKO_REPO_ROOT=/opt/kikoteam \
    KIKO_STORAGE_LOCAL_ROOT=/mnt/kikoteam \
    KIKO_CONTEXT_ROOT=/mnt/context \
    KIKO_RUNTIME_TEMP_ROOT=/var/tmp/kikoteam/temp
RUN install -d -o node -g node /mnt/kikoteam && install -d -m 1777 /mnt/context /var/tmp/kikoteam/temp
WORKDIR /opt/kikoteam
COPY --from=go-build /out/agent-engine ./agent-engine/bin/agent-engine
COPY --from=go-build /out/sandbox-runner ./sandbox-runner/bin/sandbox-runner
COPY --from=node-build /src/agent-engine/node_modules ./agent-engine/node_modules
COPY agent-engine/scripts/pi.sh ./agent-engine/scripts/pi.sh
COPY agent-engine/extensions/capability-policy.mjs agent-engine/extensions/context-receipt.mjs agent-engine/extensions/human-tools.mjs ./agent-engine/extensions/
WORKDIR /opt/kikoteam/sandbox-runner/builtin-mcp
COPY --from=node-build /src/builtin-mcp/package.json ./
COPY --from=node-build /src/builtin-mcp/dist ./dist
COPY --from=node-build /src/builtin-mcp/vendor/pi-tools/lib ./vendor/pi-tools/lib
COPY --from=node-build /src/builtin-mcp/vendor/pi-tools/LICENSE ./vendor/pi-tools/LICENSE
COPY --from=node-build /src/builtin-mcp/node_modules ./node_modules
WORKDIR /opt/kikoteam
USER node
ENTRYPOINT ["/opt/kikoteam/agent-engine/bin/agent-engine"]
