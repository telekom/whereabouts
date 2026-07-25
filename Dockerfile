FROM golang:1.26.5@sha256:079e59808d2d252516e27e3f3a9c003740dee7f75e55aa71528766d52bcfc16a AS builder
WORKDIR /go/src/github.com/telekom/whereabouts
# Version information injected at build time via --build-arg
ARG VERSION=""
ARG GIT_SHA=""
ARG GIT_TREE_STATE="clean"
ARG RELEASE_STATUS="unreleased"
# Cache dependency downloads in a separate layer
COPY go.mod go.sum ./
COPY vendor/ vendor/
# Copy source code
COPY . .
RUN VERSION_LDFLAGS="-X github.com/telekom/whereabouts/pkg/version.Version=${VERSION} \
    -X github.com/telekom/whereabouts/pkg/version.GitSHA=${GIT_SHA} \
    -X github.com/telekom/whereabouts/pkg/version.GitTreeState=${GIT_TREE_STATE} \
    -X github.com/telekom/whereabouts/pkg/version.ReleaseStatus=${RELEASE_STATUS}" && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w ${VERSION_LDFLAGS}" -o bin/whereabouts ./cmd/whereabouts/ && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w ${VERSION_LDFLAGS}" -o bin/whereabouts-operator ./cmd/operator/ && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w ${VERSION_LDFLAGS}" -o bin/install-cni ./cmd/install-cni/

<<<<<<< HEAD
FROM gcr.io/distroless/static:nonroot@sha256:d29e660cc75a5b6b1334e03c5c81ccf9bc0884a002c6000dbf0fb96034814478
LABEL org.opencontainers.image.source=https://github.com/telekom/whereabouts
WORKDIR /
COPY --from=builder /go/src/github.com/telekom/whereabouts/bin/whereabouts .
COPY --from=builder /go/src/github.com/telekom/whereabouts/bin/whereabouts-operator .
COPY --from=builder /go/src/github.com/telekom/whereabouts/bin/install-cni .
# Default to non-root; the DaemonSet overrides this via securityContext.
USER 65532:65532
CMD ["/install-cni"]
=======
FROM alpine:3.24.1
LABEL org.opencontainers.image.source=https://github.com/k8snetworkplumbingwg/whereabouts
COPY --from=0 /go/src/github.com/k8snetworkplumbingwg/whereabouts/bin/whereabouts .
COPY --from=0 /go/src/github.com/k8snetworkplumbingwg/whereabouts/bin/ip-control-loop .
COPY --from=0 /go/src/github.com/k8snetworkplumbingwg/whereabouts/bin/ip-reconciler .
COPY --from=0 /go/src/github.com/k8snetworkplumbingwg/whereabouts/bin/node-slice-controller .
COPY script/install-cni.sh .
COPY script/lib.sh .
COPY script/token-watcher.sh .
CMD ["/install-cni.sh"]
>>>>>>> upstream/master
