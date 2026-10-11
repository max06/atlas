# atlas: the CLI plus the toolchain it drives, as one image.
#
# One image serves three uses: the developer wrapper (contrib/atlas-wrapper.sh,
# copied into consumer repos as bin/atlas), CI review steps, and the Argo CD
# config management plugin sidecar. The toolchain versions are the ones the CLI
# is tested with (internal/toolchain), so `atlas doctor` reports "ok".
#
# Writable state lives under /cache (mode 1777): the wrapper mounts a named
# volume there and runs as the caller's uid. Docker copies the mode into a new
# named volume, so any uid can write to it.

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/max06/atlas/internal/cli.Version=${VERSION}" \
      -o /out/atlas ./cmd/atlas

FROM alpine:3.24

RUN apk add --no-cache bash ca-certificates curl git gnupg

# renovate: datasource=github-releases depName=helm/helm
ARG HELM_VERSION=v4.2.3
# renovate: datasource=github-releases depName=helmfile/helmfile
ARG HELMFILE_VERSION=v1.7.3
# renovate: datasource=github-releases depName=getsops/sops
ARG SOPS_VERSION=v3.12.2
# renovate: datasource=github-releases depName=kubernetes-sigs/kustomize extractVersion=^kustomize/(?<version>.+)$
ARG KUSTOMIZE_VERSION=v5.6.0

COPY .github/actions/atlas-render/install-tools.sh /install-tools.sh
RUN sh /install-tools.sh \
      helm=${HELM_VERSION} \
      helmfile=${HELMFILE_VERSION} \
      sops=${SOPS_VERSION} \
      kustomize=${KUSTOMIZE_VERSION} && \
    rm /install-tools.sh

# Redaction post-renderer (atlas render --redact). Plugins are read-only data;
# HELM_PLUGINS points at them, so they work for any uid.
COPY .github/actions/atlas-render/atlas-redact/ /opt/helm-plugins/atlas-redact/
RUN chmod +x /opt/helm-plugins/atlas-redact/atlas-redact.sh

# Every writable path under /cache: helm repositories and chart cache,
# helmfile remote states, git config. HOME too, for tools that insist on it.
RUN mkdir -p /cache && chmod 1777 /cache
ENV HOME=/cache/home \
    XDG_CACHE_HOME=/cache \
    XDG_CONFIG_HOME=/cache/config \
    XDG_DATA_HOME=/cache/data \
    HELM_CACHE_HOME=/cache/helm \
    HELM_CONFIG_HOME=/cache/helm-config \
    HELM_PLUGINS=/opt/helm-plugins \
    HELMFILE_UPGRADE_NOTICE_DISABLED=1
VOLUME /cache

COPY --from=build /out/atlas /usr/local/bin/atlas

ENTRYPOINT ["atlas"]
