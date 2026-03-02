CURPATH=$(PWD)
BIN_DIR=$(CURPATH)/bin

IMAGE_NAME ?= whereabouts
IMAGE_REGISTRY ?= ghcr.io/telekom
IMAGE_PULL_POLICY ?= Always
IMAGE_TAG ?= latest
COMPUTE_NODES ?= 2

OCI_BIN ?= docker


build:
	hack/build-go.sh

docker-build:
	$(OCI_BIN) build --build-arg VERSION=$(IMAGE_TAG) --build-arg GIT_SHA=$$(git rev-parse --short HEAD) -t ${IMAGE_REGISTRY}/${IMAGE_NAME}:${IMAGE_TAG} -f Dockerfile .

generate-api:
	hack/update-codegen.sh
	rm -rf github.com

install-tools:
	hack/install-kubebuilder-tools.sh

test: build install-tools
	hack/test-go.sh 

test-skip-static: build
	hack/test-go.sh --skip-static-check 

kind:
	hack/e2e-setup-kind-cluster.sh -n $(COMPUTE_NODES)

update-deps:
	go mod tidy
	go mod vendor
	go mod verify

lint:
	golangci-lint run --timeout=5m ./...

lint-fix:
	golangci-lint run --timeout=5m --fix ./...

$(BIN_DIR):
	@mkdir -p $(BIN_DIR)

YQ=$(BIN_DIR)/yq
YQ_VERSION=v4.44.1
$(YQ): | $(BIN_DIR); $(info installing yq)
	@OS=$$(uname -s | tr '[:upper:]' '[:lower:]') && ARCH=$$(uname -m | sed 's/x86_64/amd64/' | sed 's/aarch64/arm64/' | sed 's/arm64/arm64/') && \
	curl -fsSL -o $(YQ) https://github.com/mikefarah/yq/releases/download/$(YQ_VERSION)/yq_$${OS}_$${ARCH} && chmod +x $(YQ)

.PHONY: chart-prepare-release
chart-prepare-release: | $(YQ) ; ## prepare chart for release
	@GITHUB_TAG=$(GITHUB_TAG) GITHUB_TOKEN=$(GITHUB_TOKEN) GITHUB_REPO_OWNER=$(GITHUB_REPO_OWNER) hack/release/chart-update.sh

.PHONY: chart-push-release
chart-push-release: ## push release chart
	@GITHUB_TAG=$(GITHUB_TAG) GITHUB_TOKEN=$(GITHUB_TOKEN) GITHUB_REPO_OWNER=$(GITHUB_REPO_OWNER) hack/release/chart-push.sh
