VERSION ?= 0.0.0-dev
IMG ?= ghcr.io/reidops/external-ddns:$(VERSION)
CHART := charts/external-ddns
YEAR ?= $(shell date +%Y)
CONTAINER_TOOL ?= docker

SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

# CRDs come from api/ only; internal/dnsendpoint declares external-dns's type
# and must not produce a CRD. The chart reads CRDs and the ClusterRole from
# verbatim copies so `make verify` catches drift.
.PHONY: manifests
manifests: controller-gen ## Generate CRDs and RBAC, and copy them into the chart.
	"$(CONTROLLER_GEN)" crd paths="./api/..." output:crd:artifacts:config=config/crd/bases
	"$(CONTROLLER_GEN)" rbac:roleName=manager-role paths="./internal/..."
	mkdir -p $(CHART)/generated/crds
	cp config/rbac/role.yaml $(CHART)/generated/role.yaml
	cp config/crd/bases/*.yaml $(CHART)/generated/crds/

.PHONY: generate
generate: controller-gen ## Generate DeepCopy methods.
	"$(CONTROLLER_GEN)" object:headerFile="hack/boilerplate.go.txt",year=$(YEAR) paths="./api/..."

.PHONY: fmt
fmt: ## Run go fmt.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet ## Run unit tests.
	go test -race ./api/... ./internal/... -coverprofile cover.out

.PHONY: test-integration
test-integration: manifests generate fmt vet setup-envtest ## Run the envtest suite against a real API server.
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" go test -tags=integration ./test/integration/ -v -ginkgo.v

.PHONY: verify
verify: manifests generate fmt ## Regenerate everything and fail if the tree is dirty.
	@status=$$(git status --porcelain) || { \
		echo "verify: git status failed; in a container, mark the workspace safe first."; \
		exit 1; \
	}; \
	if [ -n "$$status" ]; then \
		echo "Generated artifacts are out of date. Run 'make manifests generate fmt' and commit."; \
		git --no-pager diff --stat; \
		exit 1; \
	fi

.PHONY: ci
ci: verify lint test test-integration helm-lint test-e2e ## Everything a pull request must pass.

.PHONY: lint
lint: golangci-lint ## Run golangci-lint.
	"$(GOLANGCI_LINT)" run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint with fixes.
	"$(GOLANGCI_LINT)" run --fix

##@ E2E

KIND_CLUSTER ?= external-ddns-e2e
E2E_NAMESPACE := external-ddns-system
# Never "latest": the kubelet would pull instead of using the loaded image.
E2E_IMG ?= example.com/external-ddns:e2e
# A release points the suite at the candidate it is about to publish, so what
# ships is the image the suite passed and not a rebuild of the same source.
E2E_IMG_PULL ?= false
E2E_KUBECONFIG ?= $(LOCALBIN)/kubeconfig-e2e

.PHONY: setup-test-e2e
setup-test-e2e: ## Create the Kind cluster if it does not exist.
	@command -v $(KIND) >/dev/null 2>&1 || { echo "kind is not installed"; exit 1; }
	@case "$$($(KIND) get clusters)" in \
		*"$(KIND_CLUSTER)"*) echo "Kind cluster '$(KIND_CLUSTER)' exists." ;; \
		*) $(KIND) create cluster --name $(KIND_CLUSTER) ;; \
	esac

.PHONY: e2e-images
e2e-images: ## Put E2E_IMG in Kind: built from this tree, or pulled when E2E_IMG_PULL=true.
	@if [ "$(E2E_IMG_PULL)" = "true" ]; then \
		docker pull "$(E2E_IMG)"; \
	else \
		$(MAKE) docker-build IMG=$(E2E_IMG); \
	fi
	$(KIND) load docker-image $(E2E_IMG) --name $(KIND_CLUSTER)

.PHONY: dev-up
dev-up: setup-test-e2e manifests generate e2e-images helm-install ## Kind cluster with the chart, external-dns and fixtures deployed.
	@img='$(E2E_IMG)'; \
	"$(HELM)" upgrade --install external-ddns $(CHART) \
		--namespace $(E2E_NAMESPACE) --create-namespace \
		--set image.repository="$${img%:*}" --set image.tag="$${img##*:}"
	kubectl -n $(E2E_NAMESPACE) rollout restart deployment/external-ddns
	kubectl -n $(E2E_NAMESPACE) rollout status deployment/external-ddns --timeout=120s
	hack/e2e/setup.sh
	@echo "Ready. Tear down with 'make dev-down'."

.PHONY: dev-down
dev-down: cleanup-test-e2e ## Tear down the dev-up cluster.

# The suite is handed Kind's kubeconfig explicitly: controller-runtime prefers
# in-cluster credentials, and a CI runner that is itself a pod has those.
.PHONY: test-e2e
test-e2e: dev-up fmt vet ## Provision Kind, deploy, run the e2e suite, tear down.
	@status=0; \
	$(KIND) get kubeconfig --name $(KIND_CLUSTER) > $(E2E_KUBECONFIG); \
	KUBECONFIG=$(E2E_KUBECONFIG) go test -tags=e2e ./test/e2e/ -v -ginkgo.v || status=$$?; \
	$(MAKE) cleanup-test-e2e; \
	exit $$status

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## Delete the Kind cluster.
	@$(KIND) delete cluster --name $(KIND_CLUSTER)

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build the manager binary.
	go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o bin/manager ./cmd

.PHONY: run
run: manifests generate fmt vet ## Run the manager from the host against the current kubecontext.
	go run ./cmd

.PHONY: docker-build
docker-build: ## Build the image for the host platform.
	$(CONTAINER_TOOL) build --build-arg VERSION=$(VERSION) -t $(IMG) .

PLATFORMS ?= linux/arm64,linux/amd64
.PHONY: docker-buildx
docker-buildx: ## Build and push a multi-platform image (IMG, PLATFORMS).
	$(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) --build-arg VERSION=$(VERSION) --tag $(IMG) .

.PHONY: helm-lint
helm-lint: manifests helm-install ## Lint the chart and render it with the optional pieces on.
	"$(HELM)" lint $(CHART)
	"$(HELM)" template external-ddns $(CHART) > /dev/null
	"$(HELM)" template external-ddns $(CHART) \
		--set metrics.serviceMonitor.enabled=true --set metrics.prometheusRule.enabled=true \
		--set publicAddress.enabled=true \
		--set-json 'publicAddress.spec={"observers":[{"name":"a","static":{"address":"203.0.113.1"}},{"name":"b","stun":{"servers":["stun.example:3478"]}}],"publish":{"dnsEndpoint":{"namespace":"x","name":"y"},"records":["a.example"]}}' > /dev/null

.PHONY: helm-package
helm-package: manifests helm-install ## Package the chart into dist/ at VERSION.
	mkdir -p dist
	"$(HELM)" package $(CHART) --version $(VERSION) --app-version $(VERSION) -d dist

.PHONY: helm-push
helm-push: helm-package ## Push the packaged chart to oci://ghcr.io/reidops/charts.
	"$(HELM)" push dist/external-ddns-$(VERSION).tgz oci://ghcr.io/reidops/charts

##@ Dependencies

LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"

KUBECTL ?= kubectl
KIND ?= kind
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint
HELM ?= $(LOCALBIN)/helm

CONTROLLER_TOOLS_VERSION ?= v0.21.0
GOLANGCI_LINT_VERSION ?= v2.12.2
HELM_VERSION ?= v3.16.4

ENVTEST_VERSION ?= $(shell v='$(call gomodver,sigs.k8s.io/controller-runtime)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_VERSION manually" >&2; exit 1; }; \
  printf '%s\n' "$$v")

ENVTEST_K8S_VERSION ?= $(shell v='$(call gomodver,k8s.io/api)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_K8S_VERSION manually" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/')

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN)
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest
	@"$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path >/dev/null

.PHONY: envtest
envtest: $(ENVTEST)
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT)
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))
	@test -f .custom-gcl.yml && { \
		$(GOLANGCI_LINT) custom --destination $(LOCALBIN) --name golangci-lint-custom && \
		mv -f $(LOCALBIN)/golangci-lint-custom $(GOLANGCI_LINT); \
	} || true

# The CI Go job runs in a toolchain container with no helm.
.PHONY: helm-install
helm-install: $(LOCALBIN)
	@test -x "$(HELM)" || { \
		os=$$(uname | tr '[:upper:]' '[:lower:]'); arch=$$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/'); \
		curl -fsSL "https://get.helm.sh/helm-$(HELM_VERSION)-$$os-$$arch.tar.gz" | tar -xzO "$$os-$$arch/helm" > "$(HELM)"; \
		chmod +x "$(HELM)"; \
	}

define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f "$(1)" ;\
GOBIN="$(LOCALBIN)" go install $${package} ;\
mv "$(LOCALBIN)/$$(basename "$(1)")" "$(1)-$(3)" ;\
} ;\
ln -sf "$$(realpath "$(1)-$(3)")" "$(1)"
endef

define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef
