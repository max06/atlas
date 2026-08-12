# atlas-render: renders one side of an ATLAS helmfile deployment.
#
# Tools baked in: helm, helmfile, sops, yq, jq, git.
# Versions pinned below — Renovate manages bumps via regex.
FROM alpine:3.24

RUN apk add --no-cache bash curl git jq

# renovate: datasource=github-releases depName=helm/helm
ARG HELM_VERSION=v4.2.3
# renovate: datasource=github-releases depName=helmfile/helmfile
ARG HELMFILE_VERSION=v1.7.3
# renovate: datasource=github-releases depName=getsops/sops
ARG SOPS_VERSION=v3.12.2
# renovate: datasource=github-releases depName=mikefarah/yq
ARG YQ_VERSION=v4.53.2
# renovate: datasource=github-releases depName=kubernetes-sigs/kustomize extractVersion=^kustomize/(?<version>.+)$
ARG KUSTOMIZE_VERSION=v5.6.0

COPY install-tools.sh /install-tools.sh
RUN chmod +x /install-tools.sh && \
    /install-tools.sh \
      helm=${HELM_VERSION} \
      helmfile=${HELMFILE_VERSION} \
      sops=${SOPS_VERSION} \
      yq=${YQ_VERSION} \
      kustomize=${KUSTOMIZE_VERSION} && \
    rm /install-tools.sh

# helmfile init downloads helm plugins (diff, secrets, etc.) on first run.
# Run it at build time so the image is ready to go.
RUN helmfile init --force 2>/dev/null || true

# `helmfile version` phones home to github.com for an update notice; ATLAS
# execs it on every render for the version gate, so a transient TLS/network
# failure toward github.com would fail the whole render. Disable the notice —
# renders should only need the network for actual chart fetches.
ENV HELMFILE_UPGRADE_NOTICE_DISABLED=1

# Bake the atlas-redact Helm post-renderer plugin into the image.
COPY atlas-redact/ /atlas-plugins/atlas-redact/
RUN chmod +x /atlas-plugins/atlas-redact/atlas-redact.sh

COPY entrypoint.sh /entrypoint.sh
COPY render.sh /render.sh
RUN chmod +x /entrypoint.sh /render.sh

ENTRYPOINT ["/entrypoint.sh"]
