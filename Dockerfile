FROM golang:1.25.7@sha256:931c889bca758a82fcbfcb1b6ed6ca1de30783e9e52e6093ad50060735cb99be AS builder
WORKDIR /go/src/github.com/telekom/whereabouts
# Cache dependency downloads in a separate layer
COPY go.mod go.sum ./
COPY vendor/ vendor/
# Copy source code
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/whereabouts ./cmd/ && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/ip-control-loop ./cmd/controlloop/ && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/node-slice-controller ./cmd/nodeslicecontroller/

FROM alpine:3.23.3@sha256:25109184c71bdad752c8312a8623239686a9a2071e8825f20acb8f2198c3f659
LABEL org.opencontainers.image.source=https://github.com/telekom/whereabouts
WORKDIR /
COPY --from=builder /go/src/github.com/telekom/whereabouts/bin/whereabouts .
COPY --from=builder /go/src/github.com/telekom/whereabouts/bin/ip-control-loop .
COPY --from=builder /go/src/github.com/telekom/whereabouts/bin/node-slice-controller .
COPY script/install-cni.sh .
COPY script/lib.sh .
COPY script/token-watcher.sh .
CMD ["/install-cni.sh"]
