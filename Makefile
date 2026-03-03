# ---------------------------------------------------------------
# Variables
# ---------------------------------------------------------------
CURPATH=$(PWD)
BIN_DIR=$(CURPATH)/bin

IMAGE_NAME ?= whereabouts
IMAGE_REGISTRY ?= ghcr.io/telekom
IMAGE_PULL_POLICY ?= Always
IMAGE_TAG ?= latest
COMPUTE_NODES ?= 2

OCI_BIN ?= docker
GO ?= go

# Tool versions
CONTROLLER_GEN_VERSION ?= v0.20.0
STATICCHECK_VERSION ?= v0.6.0

# Resolved tool paths
CONTROLLER_GEN := $(BIN_DIR)/controller-gen
STATICCHECK := $(BIN_DIR)/staticcheck

# ---------------------------------------------------------------
# Version information (replaces hack/build-go.sh version logic)
# ---------------------------------------------------------------
GIT_SHA := $(shell git rev-parse --short HEAD 2>/dev/null)
GIT_TREE_STATE := $(shell test -n "$$(git status --porcelain --untracked-files=no 2>/dev/null)" && echo dirty || echo clean)
GIT_TAG := $(shell git describe --tags --abbrev=0 --exact-match 2>/dev/null)
GIT_TAG_LAST := $(shell git describe --tags --abbrev=0 2>/dev/null)
VERSION ?= $(GIT_TAG_LAST)
RELEASE_STATUS := $(if $(strip $(VERSION)$(GIT_TAG)),released,unreleased)
VERSION_PKG := github.com/telekom/whereabouts/pkg/version
LDFLAGS := -X $(VERSION_PKG).Version=$(VERSION) \
           -X $(VERSION_PKG).GitSHA=$(GIT_SHA) \
           -X $(VERSION_PKG).GitTreeState=$(GIT_TREE_STATE) \
           -X $(VERSION_PKG).ReleaseStatus=$(RELEASE_STATUS)

# ---------------------------------------------------------------
# Targets
# ---------------------------------------------------------------

.PHONY: build
build: ## Build CNI plugin and operator binaries.
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/whereabouts ./cmd/
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/whereabouts-operator ./cmd/operator/

.PHONY: generate
generate: $(CONTROLLER_GEN) ## Generate CRDs, deepcopy, and clientsets/informers/listers.
	$(CONTROLLER_GEN) object crd:crdVersions=v1 paths="./pkg/api/..." output:crd:artifacts:config=doc/crds
	cp doc/crds/whereabouts.cni.cncf.io_*.yaml deployment/whereabouts-chart/crds/
	hack/update-codegen.sh
	rm -rf github.com

.PHONY: verify-codegen
verify-codegen: ## Verify generated code is up to date.
	hack/verify-codegen.sh

.PHONY: test
test: build vet lint-staticcheck ## Run unit tests (includes build, vet, staticcheck).
	$(GO) test -v -race -covermode=atomic -coverprofile=coverage.out \
		$$($(GO) list ./... | grep -v e2e | tr "\n" " ")

.PHONY: test-skip-static
test-skip-static: build vet ## Run tests without staticcheck (faster iteration).
	$(GO) test -v -race -covermode=atomic -coverprofile=coverage.out \
		$$($(GO) list ./... | grep -v e2e | tr "\n" " ")

.PHONY: vet
vet: ## Run go vet.
	$(GO) vet ./cmd/... ./pkg/... ./internal/...

.PHONY: lint-staticcheck
lint-staticcheck: $(STATICCHECK) ## Run staticcheck.
	$(STATICCHECK) ./...

.PHONY: lint
lint: ## Run golangci-lint.
	golangci-lint run --timeout=5m ./...

.PHONY: lint-fix
lint-fix: ## Run golangci-lint with auto-fix.
	golangci-lint run --timeout=5m --fix ./...

.PHONY: docker-build
docker-build: ## Build container image.
	$(OCI_BIN) build --build-arg VERSION=$(IMAGE_TAG) --build-arg GIT_SHA=$(GIT_SHA) -t $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG) -f Dockerfile .

.PHONY: kind
kind: ## Create a KinD cluster with whereabouts installed.
	hack/e2e-setup-kind-cluster.sh -n $(COMPUTE_NODES)

.PHONY: update-deps
update-deps: ## Update Go dependencies.
	$(GO) mod tidy
	$(GO) mod vendor
	$(GO) mod verify

# ---------------------------------------------------------------
# Tool installation
# ---------------------------------------------------------------
$(BIN_DIR):
	@mkdir -p $(BIN_DIR)

$(CONTROLLER_GEN): | $(BIN_DIR)
	GOBIN=$(BIN_DIR) $(GO) install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)

$(STATICCHECK): | $(BIN_DIR)
	GOBIN=$(BIN_DIR) $(GO) install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)

YQ=$(BIN_DIR)/yq
YQ_VERSION=v4.44.1
$(YQ): | $(BIN_DIR); $(info installing yq)
	@OS=$$(uname -s | tr '[:upper:]' '[:lower:]') && ARCH=$$(uname -m | sed 's/x86_64/amd64/' | sed 's/aarch64/arm64/' | sed 's/arm64/arm64/') && \
	curl -fsSL -o $(YQ) https://github.com/mikefarah/yq/releases/download/$(YQ_VERSION)/yq_$${OS}_$${ARCH} && chmod +x $(YQ)

# ---------------------------------------------------------------
# Release
# ---------------------------------------------------------------
.PHONY: chart-prepare-release
chart-prepare-release: | $(YQ) ; ## Prepare chart for release.
	@GITHUB_TAG=$(GITHUB_TAG) GITHUB_TOKEN=$(GITHUB_TOKEN) GITHUB_REPO_OWNER=$(GITHUB_REPO_OWNER) hack/release/chart-update.sh

.PHONY: chart-push-release
chart-push-release: ## Push release chart.
	@GITHUB_TAG=$(GITHUB_TAG) GITHUB_TOKEN=$(GITHUB_TOKEN) GITHUB_REPO_OWNER=$(GITHUB_REPO_OWNER) hack/release/chart-push.sh
