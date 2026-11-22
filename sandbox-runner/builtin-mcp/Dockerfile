FROM node:24.18.1-trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash build-essential python3 procps ripgrep fd-find ca-certificates \
    && ln -s /usr/bin/fdfind /usr/local/bin/fd \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd -g 10001 sandbox && useradd -u 10001 -g 10001 -m sandbox
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund && npm_config_nodedir=/usr/local npm rebuild node-pty
COPY . .
RUN npm run build && chown -R 10001:10001 /app
USER 10001:10001
CMD ["node", "--disable-warning=ExperimentalWarning", "dist/http-main.js", "--config", "/run/builtin-mcp.json"]
