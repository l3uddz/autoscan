ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

FROM alpine:3.20 AS rootfs
RUN apk add --no-cache ca-certificates tzdata

FROM scratch

ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

ENV \
  PATH="/app/autoscan" \
  AUTOSCAN_CONFIG="/config/config.yml" \
  AUTOSCAN_DATABASE="/config/autoscan.db" \
  AUTOSCAN_LOG="/config/activity.log" \
  AUTOSCAN_VERBOSITY="0"

COPY --from=rootfs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=rootfs /usr/share/zoneinfo /usr/share/zoneinfo

COPY ["dist/autoscan_${TARGETOS}_${TARGETARCH}${TARGETVARIANT:+_7}/autoscan", "/app/autoscan/autoscan"]

VOLUME ["/config"]

EXPOSE 3030

ENTRYPOINT ["/app/autoscan/autoscan"]
