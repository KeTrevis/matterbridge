FROM golang:1.25-alpine AS build
RUN apk add --no-cache mailcap
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go test -p 2 -mod=vendor ./bridge/slack/... ./bridge/discord/... ./gateway/... \
    && CGO_ENABLED=0 go build -p 2 -mod=vendor -trimpath \
    -ldflags "-s -w -X github.com/42wim/matterbridge/version.GitHash=local" \
    -o /matterbridge .

FROM alpine:3.23
RUN apk add --no-cache ca-certificates mailcap && adduser -D -u 1000 bridge \
    && mkdir -p /var/lib/matterbridge && chown bridge:bridge /var/lib/matterbridge
COPY --from=build /matterbridge /usr/local/bin/matterbridge
LABEL org.opencontainers.image.source="https://github.com/KeTrevis/matterbridge"
USER bridge
ENTRYPOINT ["/usr/local/bin/matterbridge"]
CMD ["-conf", "/etc/matterbridge/matterbridge.toml"]
