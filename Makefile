SHELL := /bin/bash

RUNTIME_ROOT ?= ..
KAVRYCTL_DIR ?= $(RUNTIME_ROOT)/kavryctl
REGISTRY_DIR ?= $(RUNTIME_ROOT)/registry
GATEWAY_DIR ?= $(RUNTIME_ROOT)/gateway
OPERATOR_DIR ?= $(RUNTIME_ROOT)/k8s-operator
GOCACHE_DIR ?= $(CURDIR)/.cache/go-build
LOCAL_VERSION ?= 0.0.1-beta-local

COMPONENTS := $(KAVRYCTL_DIR) $(REGISTRY_DIR) $(GATEWAY_DIR) $(OPERATOR_DIR)
CHARTS := \
	$(KAVRYCTL_DIR)/charts/kavryctl \
	$(REGISTRY_DIR)/charts/registry \
	$(GATEWAY_DIR)/charts/gateway \
	$(OPERATOR_DIR)/charts/k8s-operator

.PHONY: help
help:
	@printf "Kavrynt integration targets:\n"
	@printf "  make qa             Validate canonical runtime repositories and charts\n"
	@printf "  make fmt-check      Check canonical Go formatting\n"
	@printf "  make test           Run canonical runtime unit tests\n"
	@printf "  make vet            Run canonical runtime go vet checks\n"
	@printf "  make docker-build   Build coordinated local runtime images\n"
	@printf "  make helm-deps      Build umbrella chart dependencies\n"
	@printf "  make helm-lint      Lint component and umbrella charts\n"
	@printf "  make helm-template  Render the umbrella chart\n"
	@printf "  make e2e-kind       Run the disposable Kind end-to-end workflow\n"

.PHONY: qa
qa: fmt-check test vet helm-lint helm-template

.PHONY: fmt-check
fmt-check:
	@failed=0; \
	for component in $(COMPONENTS); do \
		files=$$(find "$$component" -name '*.go' -not -path '*/.cache/*'); \
		if [ -n "$$files" ]; then \
			unformatted=$$(gofmt -l $$files); \
			if [ -n "$$unformatted" ]; then \
				printf "Unformatted Go files in %s:\n%s\n" "$$component" "$$unformatted"; \
				failed=1; \
			fi; \
		fi; \
	done; \
	exit $$failed

.PHONY: test
test:
	@mkdir -p "$(GOCACHE_DIR)"
	@for component in $(COMPONENTS); do \
		printf "==> go test %s\n" "$$component"; \
		(cd "$$component" && GOCACHE="$(GOCACHE_DIR)" go test ./...); \
	done

.PHONY: vet
vet:
	@mkdir -p "$(GOCACHE_DIR)"
	@for component in $(COMPONENTS); do \
		printf "==> go vet %s\n" "$$component"; \
		(cd "$$component" && GOCACHE="$(GOCACHE_DIR)" go vet ./...); \
	done

.PHONY: docker-build
docker-build:
	docker build -t kavrynt/registry:$(LOCAL_VERSION) "$(REGISTRY_DIR)"
	docker build -t kavrynt/gateway:$(LOCAL_VERSION) "$(GATEWAY_DIR)"
	docker build -t kavrynt/operator:$(LOCAL_VERSION) "$(OPERATOR_DIR)"
	docker build -t kavrynt/e2e-mcp-server:$(LOCAL_VERSION) test/e2e/mcp-server

.PHONY: helm-deps
helm-deps:
	helm dependency build --skip-refresh charts/kavrynt

.PHONY: helm-lint
helm-lint: helm-deps
	@for chart in $(CHARTS); do helm lint "$$chart"; done
	helm lint charts/kavrynt

.PHONY: helm-template
helm-template: helm-deps
	helm template kavrynt charts/kavrynt --namespace kavrynt-system

.PHONY: helm-package
helm-package: helm-deps
	mkdir -p dist/charts
	helm package charts/kavrynt --destination dist/charts

.PHONY: e2e-kind
e2e-kind:
	KAVRYCTL_REPO="$(KAVRYCTL_DIR)" \
	REGISTRY_REPO="$(REGISTRY_DIR)" \
	GATEWAY_REPO="$(GATEWAY_DIR)" \
	OPERATOR_REPO="$(OPERATOR_DIR)" \
	LOCAL_VERSION="$(LOCAL_VERSION)" \
	./scripts/e2e-kind.sh
