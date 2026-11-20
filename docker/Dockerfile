ARG TARGETOS
ARG TARGETARCH

FROM alpine:3.24 AS rootfs
RUN apk add --no-cache ca-certificates tzdata

FROM scratch

ARG TARGETOS
ARG TARGETARCH

ENV \
  PATH="/app" \
  AUTOSCAN_CONFIG="/config/config.yml" \
  AUTOSCAN_DATABASE="/config/autoscan.db" \
  AUTOSCAN_LOG="/config/activity.log" \
  AUTOSCAN_VERBOSITY="0"

COPY --from=rootfs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=rootfs /usr/share/zoneinfo /usr/share/zoneinfo

# goreleaser v2 adds a suffix to each build folder: amd64_v1, arm64_v8.0
# and arm_7. The pattern matches exactly one folder for each platform.
COPY ["dist/autoscan_${TARGETOS}_${TARGETARCH}_*/autoscan", "/app/autoscan"]

VOLUME ["/config"]

EXPOSE 3030

ENTRYPOINT ["/app/autoscan"]
