FROM golang:1.27.0@sha256:65b6f280bf050ec5af12716857e8ea8439d694dbba8f31ceeb7630670071f2bb AS builder
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

FROM gcr.io/distroless/static:nonroot@sha256:1c2c046bc09ed40fad370b599a0b1ae7987f55b01e247cf27a7c27cd97e5bbc7
LABEL org.opencontainers.image.source=https://github.com/telekom/whereabouts
WORKDIR /
COPY --from=builder /go/src/github.com/telekom/whereabouts/bin/whereabouts .
COPY --from=builder /go/src/github.com/telekom/whereabouts/bin/whereabouts-operator .
COPY --from=builder /go/src/github.com/telekom/whereabouts/bin/install-cni .
# Default to non-root; the DaemonSet overrides this via securityContext.
USER 65532:65532
CMD ["/install-cni"]
