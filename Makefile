CONTAINER_CLI ?= $(shell command -v docker >/dev/null 2>&1 && echo docker || echo podman)
BUILD_ARGS ?=
IMG ?= ghcr.io/bristol-supercomputing/module-controller:dev
NRI_IMG ?= ghcr.io/bristol-supercomputing/module-nri:dev
ENVTEST_K8S_VERSION ?= 1.36.2
CONTROLLER_TOOLS_VERSION ?= v0.21.0
SETUP_ENVTEST_VERSION ?= v0.24.1
LOCALBIN ?= $(CURDIR)/bin
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
SETUP_ENVTEST ?= $(LOCALBIN)/setup-envtest
export CGO_ENABLED ?= 0

.PHONY: all generate manifests fmt vet build test test-envtest test-e2e helm-lint image image-nri kind-up kind-down

all: build

generate: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) object paths=./api/...

manifests: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) crd paths=./api/... output:crd:dir=deploy/helm/k8s-modules/crds

fmt:
	go fmt ./...

vet:
	go vet ./...
	go vet -tags e2e ./test/e2e/...

build: generate manifests fmt vet
	go build -o $(LOCALBIN)/ ./cmd/...

test:
	go test ./api/... ./internal/... -count=1

test-envtest: manifests $(SETUP_ENVTEST)
	KUBEBUILDER_ASSETS="$$($(SETUP_ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" \
		go test ./test/envtest/... -count=1 -timeout 10m

test-e2e:
	go test -tags e2e ./test/e2e/... -count=1 -timeout 20m -v

helm-lint:
	helm lint deploy/helm/k8s-modules

image:
	$(CONTAINER_CLI) build $(BUILD_ARGS) -t $(IMG) .

image-nri:
	$(CONTAINER_CLI) build $(BUILD_ARGS) -f Dockerfile.nri -t $(NRI_IMG) .

kind-up:
	CONTAINER_CLI=$(CONTAINER_CLI) BUILD_ARGS="$(BUILD_ARGS)" deploy/kind/setup.sh

kind-down:
	CONTAINER_CLI=$(CONTAINER_CLI) deploy/kind/teardown.sh

$(CONTROLLER_GEN):
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

$(SETUP_ENVTEST):
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@$(SETUP_ENVTEST_VERSION)
