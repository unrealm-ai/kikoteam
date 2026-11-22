FROM golang:1.27.2-bookworm AS build
WORKDIR /src/event-hub
COPY event-hub/go.mod event-hub/go.sum ./
COPY event-hub/third_party ./third_party
COPY shared/trigger /src/shared/trigger
RUN go mod download
COPY event-hub/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/event-hub ./cmd/event-hub
FROM alpine:3.23
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 kiko
WORKDIR /app
COPY --from=build /out/event-hub /app/event-hub
USER kiko
ENTRYPOINT ["/app/event-hub"]
