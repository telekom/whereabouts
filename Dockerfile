FROM golang:1.26.4@sha256:792443b89f65105abba56b9bd5e97f680a80074ac62fc844a584212f8c8102c3 AS builder
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
FROM gcr.io/distroless/static:nonroot@sha256:963fa6c544fe5ce420f1f54fb88b6fb01479f054c8056d0f74cc2c6000df5240
LABEL org.opencontainers.image.source=https://github.com/telekom/whereabouts
WORKDIR /
COPY --from=builder /go/src/github.com/telekom/whereabouts/bin/whereabouts .
COPY --from=builder /go/src/github.com/telekom/whereabouts/bin/whereabouts-operator .
COPY --from=builder /go/src/github.com/telekom/whereabouts/bin/install-cni .
# Default to non-root; the DaemonSet overrides this via securityContext.
USER 65532:65532
CMD ["/install-cni"]
=======
FROM alpine:3.23.4
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
