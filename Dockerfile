FROM alpine:3.22 AS certs
RUN apk add --no-cache ca-certificates

FROM scratch

ARG SCRAP_RELEASE_SHA=unknown
ARG SCRAP_VERSION=dev
ARG SCRAP_BUILD_TIME=unknown
ARG SCRAP_DIRTY_TREE=unknown
ARG SCRAPD_IMAGE_BINARY=bin/scrapd-linux-amd64

LABEL org.opencontainers.image.title="scrapd"
LABEL org.opencontainers.image.description="S.C.R.A.P. storage gateway node"
LABEL org.opencontainers.image.source="https://github.com/petabytecl/scrap"
LABEL org.opencontainers.image.revision="${SCRAP_RELEASE_SHA}"
LABEL org.opencontainers.image.version="${SCRAP_VERSION}"
LABEL org.opencontainers.image.created="${SCRAP_BUILD_TIME}"
LABEL cl.petabyte.scrap.dirty_tree="${SCRAP_DIRTY_TREE}"

USER 65532:65532
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --chown=65532:65532 ${SCRAPD_IMAGE_BINARY} /scrapd

ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt

ENTRYPOINT ["/scrapd"]
