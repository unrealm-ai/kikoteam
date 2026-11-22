FROM node:24.19.0-bookworm-slim AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build
FROM golang:1.27.2-bookworm AS build
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
COPY shared/trigger /src/shared/trigger
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/backend ./cmd/backend
FROM alpine:3.23
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 kiko
WORKDIR /app
COPY --from=build /out/backend /app/backend
COPY --from=web /src/web/dist /app/web
USER kiko
ENTRYPOINT ["/app/backend"]
