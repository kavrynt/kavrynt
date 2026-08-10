SHELL := /bin/bash

COMPONENTS := cmd/kavryctl services/registry services/gateway operator
GOCACHE_DIR := $(CURDIR)/.cache/go-build

.PHONY: help
help:
	@printf "Kavrynt monorepo targets:\n"
	@printf "  make qa             Run format check, tests, vet, and Helm rendering\n"
	@printf "  make fmt-check      Check Go formatting\n"
	@printf "  make fmt            Format Go files\n"
	@printf "  make test           Run Go tests for every component\n"
	@printf "  make vet            Run go vet for every component\n"
	@printf "  make docker-build   Build development Docker images\n"
	@printf "  make helm-lint      Lint Helm charts\n"
	@printf "  make helm-template  Render Helm charts\n"

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

.PHONY: fmt
fmt:
	@for component in $(COMPONENTS); do \
		files=$$(find "$$component" -name '*.go' -not -path '*/.cache/*'); \
		if [ -n "$$files" ]; then gofmt -w $$files; fi; \
	done

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
	docker build -t kavrynt/kavryctl:dev ./cmd/kavryctl
	docker build -t kavrynt/registry:dev ./services/registry
	docker build -t kavrynt/gateway:dev ./services/gateway
	docker build -t kavrynt/k8s-operator:dev ./operator

.PHONY: helm-lint
helm-lint:
	helm lint cmd/kavryctl/charts/kavryctl
	helm lint services/registry/charts/registry
	helm lint services/gateway/charts/gateway
	helm lint operator/charts/k8s-operator

.PHONY: helm-template
helm-template:
	helm template kavryctl cmd/kavryctl/charts/kavryctl
	helm template registry services/registry/charts/registry
	helm template gateway services/gateway/charts/gateway
	helm template k8s-operator operator/charts/k8s-operator
