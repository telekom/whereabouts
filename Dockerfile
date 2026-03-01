FROM golang:1.25.6 AS builder
WORKDIR /go/src/github.com/telekom/whereabouts
# Cache dependency downloads in a separate layer
COPY go.mod go.sum ./
COPY vendor/ vendor/
# Copy source code
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/whereabouts ./cmd/ && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/ip-control-loop ./cmd/controlloop/ && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/node-slice-controller ./cmd/nodeslicecontroller/

FROM alpine:3.23.3
LABEL org.opencontainers.image.source=https://github.com/telekom/whereabouts
COPY --from=builder /go/src/github.com/telekom/whereabouts/bin/whereabouts .
COPY --from=builder /go/src/github.com/telekom/whereabouts/bin/ip-control-loop .
COPY --from=builder /go/src/github.com/telekom/whereabouts/bin/node-slice-controller .
COPY script/install-cni.sh .
COPY script/lib.sh .
COPY script/token-watcher.sh .
CMD ["/install-cni.sh"]
