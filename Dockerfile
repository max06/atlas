# atlas: the CLI plus the toolchain it drives, as one image.
#
# One image serves three uses: the developer wrapper (contrib/atlas-wrapper.sh,
# copied into consumer repos as bin/atlas), the review actions (atlas-render,
# atlas-diff: their action.yml runs this image with an own entrypoint), and the
# Argo CD config management plugin sidecar. The toolchain versions are the ones the CLI
# is tested with (internal/toolchain), so `atlas doctor` reports "ok".
#
# Writable state lives under /cache (mode 1777): the wrapper mounts a named
# volume there and runs as the caller's uid. Docker copies the mode into a new
# named volume, so any uid can write to it.

# Built for linux/amd64 and linux/arm64 (Apple silicon runs arm64 natively).
# The Go build and the tool downloads run on the build platform and target
# TARGETARCH, so they need no emulation; only the final stage's apk runs
# emulated when cross-building.
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
ARG VERSION=dev
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
      -ldflags "-s -w -X github.com/max06/atlas/internal/cli.Version=${VERSION}" \
      -o /out/atlas ./cmd/atlas

FROM --platform=$BUILDPLATFORM alpine:3.24 AS tools
RUN apk add --no-cache curl

# renovate: datasource=github-releases depName=helm/helm
ARG HELM_VERSION=v4.3.0
# renovate: datasource=github-releases depName=helmfile/helmfile
ARG HELMFILE_VERSION=v1.8.1
# renovate: datasource=github-releases depName=getsops/sops
ARG SOPS_VERSION=v3.13.3
# renovate: datasource=github-releases depName=kubernetes-sigs/kustomize extractVersion=^kustomize/(?<version>.+)$
ARG KUSTOMIZE_VERSION=v5.8.3
# renovate: datasource=github-releases depName=mikefarah/yq
ARG YQ_VERSION=v4.54.1
# renovate: datasource=github-releases depName=homeport/dyff
ARG DYFF_VERSION=v1.12.0

ARG TARGETARCH
COPY .github/actions/atlas-render/install-tools.sh /install-tools.sh
COPY .github/actions/atlas-diff/install-tools.sh /install-tools-diff.sh
RUN TARGETARCH=${TARGETARCH} sh /install-tools.sh \
      helm=${HELM_VERSION} \
      helmfile=${HELMFILE_VERSION} \
      sops=${SOPS_VERSION} \
      yq=${YQ_VERSION} \
      kustomize=${KUSTOMIZE_VERSION} && \
    TARGETARCH=${TARGETARCH} sh /install-tools-diff.sh dyff=${DYFF_VERSION}

FROM alpine:3.24

# No gnupg, on purpose: SOPS decrypts with age in every use of this image
# (wrapper, CI, Argo CD plugin). With a gpg binary present, sops tries the PGP
# recipients first; in CI, gpg state in the shared /github/home of one step
# made gpg -d hang forever in the next step. Without gpg the PGP attempt fails
# at once and age decrypts.
RUN apk add --no-cache bash ca-certificates curl git jq

COPY --from=tools /usr/local/bin/ /usr/local/bin/

# Redaction post-renderer (atlas render --redact). Plugins are read-only data;
# HELM_PLUGINS points at them, so they work for any uid.
COPY .github/actions/atlas-render/atlas-redact/ /opt/helm-plugins/atlas-redact/
RUN chmod +x /opt/helm-plugins/atlas-redact/atlas-redact.sh

# Review actions: atlas-render and atlas-diff run this image with
# `atlas review ci action render|diff` (their action.yml args).

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

# Argo CD plugin mode: the sidecar runs /var/run/argocd/argocd-cmp-server (from
# the argocd image's shared volume), which reads this fixed config path.
COPY contrib/argocd-cmp/plugin.yaml /home/argocd/cmp-server/config/plugin.yaml

COPY --from=build /out/atlas /usr/local/bin/atlas

ENTRYPOINT ["atlas"]
