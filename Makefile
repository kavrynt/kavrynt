SHELL := /bin/bash

LOCAL_VERSION ?= 0.0.2-beta.1-local
IMAGES := gateway operator
CHART := charts/kavrynt
GO_DIRS := api cmd internal test

STATICCHECK_VERSION ?= v0.8.0
GOSEC_VERSION ?= v2.28.0
GOVULNCHECK_VERSION ?= v1.7.0

.PHONY: help
help:
	@printf "Kavrynt runtime targets:\n"
	@printf "  make qa             Run all local quality and security gates\n"
	@printf "  make fmt-check      Check Go formatting\n"
	@printf "  make test           Run unit tests with the race detector\n"
	@printf "  make vet            Run go vet\n"
	@printf "  make lint           Run staticcheck\n"
	@printf "  make sast           Run gosec\n"
	@printf "  make vulncheck      Run govulncheck\n"
	@printf "  make helm-lint      Lint the Helm chart\n"
	@printf "  make helm-template  Render the Helm chart\n"
	@printf "  make build          Build kavryctl, gateway, and operator binaries\n"
	@printf "  make docker-build   Build local runtime images\n"
	@printf "  make e2e-kind       Run the disposable Kind end-to-end workflow\n"
	@printf "  make e2e-upgrade    Upgrade from the last Registry-based runtime in Kind\n"
	@printf "  make e2e-release    Validate a published chart and runtime images\n"

.PHONY: qa
qa: release-contract fmt-check test vet lint sast vulncheck helm-lint helm-template

.PHONY: release-contract
release-contract:
	bash ./scripts/verify-release-contract.sh

.PHONY: fmt-check
fmt-check:
	@unformatted="$$(gofmt -l $(GO_DIRS))"; \
	if [ -n "$$unformatted" ]; then printf "Unformatted Go files:\n%s\n" "$$unformatted"; exit 1; fi

.PHONY: test
test:
	go test -race ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: lint
lint:
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...

.PHONY: sast
sast:
	go run github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION) -exclude-dir=.cache ./...

.PHONY: vulncheck
vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

.PHONY: helm-lint
helm-lint:
	helm lint $(CHART)

.PHONY: helm-template
helm-template:
	helm template kavrynt $(CHART) --namespace kavrynt-system >/dev/null

.PHONY: build
build:
	@mkdir -p bin
	@for component in kavryctl $(IMAGES); do \
		printf "==> go build %s\n" "$$component"; \
		CGO_ENABLED=0 go build -trimpath -o "bin/$$component" "./cmd/$$component" || exit 1; \
	done

.PHONY: docker-build
docker-build:
	@for image in $(IMAGES); do \
		docker build --file build/Dockerfile --target "$$image" \
			--build-arg VERSION=$(LOCAL_VERSION) \
			--tag "kavrynt/$$image:$(LOCAL_VERSION)" . || exit 1; \
	done
	docker build --tag kavrynt/e2e-mcp-server:$(LOCAL_VERSION) test/e2e/mcp-server

.PHONY: helm-package
helm-package:
	mkdir -p dist/charts
	helm package $(CHART) --destination dist/charts

.PHONY: e2e-kind
e2e-kind:
	LOCAL_VERSION="$(LOCAL_VERSION)" ./scripts/e2e-kind.sh

.PHONY: e2e-upgrade
e2e-upgrade:
	./scripts/e2e-upgrade.sh

.PHONY: e2e-release
e2e-release:
	RELEASE_VERSION="$(RELEASE_VERSION)" \
	bash ./scripts/e2e-release.sh
