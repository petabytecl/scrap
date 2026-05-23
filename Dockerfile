FROM scratch

ARG SCRAP_RELEASE_SHA=unknown
ARG SCRAP_VERSION=dev
ARG SCRAP_BUILD_TIME=unknown
ARG SCRAP_DIRTY_TREE=unknown

LABEL org.opencontainers.image.title="scrapd"
LABEL org.opencontainers.image.description="S.C.R.A.P. storage gateway node"
LABEL org.opencontainers.image.source="https://github.com/petabytecl/scrap"
LABEL org.opencontainers.image.revision="${SCRAP_RELEASE_SHA}"
LABEL org.opencontainers.image.version="${SCRAP_VERSION}"
LABEL org.opencontainers.image.created="${SCRAP_BUILD_TIME}"
LABEL cl.petabyte.scrap.dirty_tree="${SCRAP_DIRTY_TREE}"

USER 65532:65532
COPY --chown=65532:65532 bin/scrapd-linux-amd64 /scrapd

ENTRYPOINT ["/scrapd"]
