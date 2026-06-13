# atlas-diff: diff rendered ATLAS manifests, replay redaction maps, build PR comment.
#
# Lighter than atlas-render — no helm/helmfile/sops needed.
# Tools baked in: dyff, yq, jq.
FROM alpine:3.24

RUN apk add --no-cache bash curl jq

# renovate: datasource=github-releases depName=mikefarah/yq
ARG YQ_VERSION=v4.53.2
# renovate: datasource=github-releases depName=homeport/dyff
ARG DYFF_VERSION=v1.12.0

COPY install-tools.sh /install-tools.sh
RUN chmod +x /install-tools.sh && \
    /install-tools.sh \
      yq=${YQ_VERSION} \
      dyff=${DYFF_VERSION} && \
    rm /install-tools.sh

# scrub-baseline.sh is used by the replay step to scrub baseline renders.
COPY scrub-baseline.sh /scrub-baseline.sh

COPY entrypoint.sh /entrypoint.sh
COPY replay.sh /replay.sh
COPY diff.sh /diff.sh
COPY comment.sh /comment.sh
RUN chmod +x /entrypoint.sh /replay.sh /diff.sh /comment.sh /scrub-baseline.sh

ENTRYPOINT ["/entrypoint.sh"]
