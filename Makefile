SHELL := /bin/bash

LOCAL_VERSION ?= 1.0.0-beta.3-local
IMAGES := gateway operator
CHART := charts/kavrynt
# The chart refuses to render without MCP client authentication settings or
# the explicit opt-out (SPEC-0014); lint and template use the opt-out.
HELM_CHECK_VALUES := --set gateway.auth.enabled=false
GO_DIRS := api cmd examples internal test

STATICCHECK_VERSION ?= v0.8.2
GOSEC_VERSION ?= v2.30.0
GOVULNCHECK_VERSION ?= v1.7.0
HELM_UNITTEST_VERSION ?= v1.1.2

.PHONY: help
help:
	@printf "Kavrynt runtime targets:\n"
	@printf "  make qa             Run all local quality and security gates\n"
	@printf "  make spec-check     Check specs and requirement-to-test traceability\n"
	@printf "  make spec-new NAME=x  Start a new spec from the template\n"
	@printf "  make fmt-check      Check Go formatting\n"
	@printf "  make test           Run unit tests with the race detector\n"
	@printf "  make console-test   Run the web console unit tests (Node 22+)\n"
	@printf "  make vet            Run go vet\n"
	@printf "  make lint           Run staticcheck\n"
	@printf "  make sast           Run gosec\n"
	@printf "  make vulncheck      Run govulncheck\n"
	@printf "  make helm-lint      Lint the Helm chart\n"
	@printf "  make helm-template  Render the Helm chart\n"
	@printf "  make helm-unittest  Run the chart unit tests (helm-unittest)\n"
	@printf "  make helm-plugins   Install the pinned helm-unittest plugin\n"
	@printf "  make alerts-test    Test the alert rules (promtool, runbooks) and notifications (amtool) (needs Docker)\n"
	@printf "  make build          Build kavryctl, gateway, and operator binaries\n"
	@printf "  make docker-build   Build local runtime images\n"
	@printf "  make e2e-kind       Run the disposable Kind end-to-end workflow\n"
	@printf "  make e2e-release    Validate a published chart and runtime images\n"
	@printf "  make demo-up        Create the Kind demo cluster (docs/DEMO.md)\n"
	@printf "  make demo-walkthrough  Run the narrated demo (DEMO_AUTO=1 to rehearse unattended)\n"
	@printf "  make demo-forward   Port-forward the Gateway console of the demo cluster\n"
	@printf "  make demo-down      Delete the Kind demo cluster\n"

.PHONY: qa
qa: release-contract spec-check fmt-check test console-test vet lint sast vulncheck helm-lint helm-template helm-unittest alerts-test

.PHONY: release-contract
release-contract:
	bash ./scripts/verify-release-contract.sh

.PHONY: spec-check
spec-check:
	bash ./scripts/verify-specs.sh

# Alert rules: runbook coverage and promtool tests (SPEC-0010); notifications:
# amtool checks of the AlertmanagerConfig (SPEC-0022). Both tools run from
# pinned images.
.PHONY: alerts-test
alerts-test:
	bash ./scripts/test-alerts.sh
	bash ./scripts/test-notifications.sh

.PHONY: spec-new
spec-new:
	bash ./scripts/new-spec.sh "$(NAME)"

.PHONY: fmt-check
fmt-check:
	@unformatted="$$(gofmt -l $(GO_DIRS))"; \
	if [ -n "$$unformatted" ]; then printf "Unformatted Go files:\n%s\n" "$$unformatted"; exit 1; fi

.PHONY: test
test:
	go test -race ./...
	cd examples/demo/mcp-server && go test -race ./...

# The console's pure modules are tested with Node's built-in test runner; no
# npm packages are installed.
.PHONY: console-test
console-test:
	node --test 'test/console/*.test.mjs'

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
	helm lint $(CHART) $(HELM_CHECK_VALUES)

.PHONY: helm-template
helm-template:
	helm template kavrynt $(CHART) --namespace kavrynt-system $(HELM_CHECK_VALUES) >/dev/null

.PHONY: helm-unittest
helm-unittest:
	@helm plugin list | grep -q '^unittest' || { printf "helm-unittest is not installed; run make helm-plugins\n"; exit 1; }
	helm unittest $(CHART)

# Helm 4 verifies plugin provenance by default; helm-unittest is installed
# from its pinned Git tag, which carries none.
.PHONY: helm-plugins
helm-plugins:
	@if helm plugin list | grep -q '^unittest'; then \
		printf "helm-unittest already installed\n"; \
	else \
		verify=""; case "$$(helm version --short)" in v4*) verify="--verify=false" ;; esac; \
		helm plugin install https://github.com/helm-unittest/helm-unittest --version $(HELM_UNITTEST_VERSION) $$verify; \
	fi

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

.PHONY: demo-up demo-walkthrough demo-forward demo-down
demo-up:
	./scripts/demo-kind.sh up

demo-walkthrough:
	./scripts/demo-kind.sh walkthrough

demo-forward:
	./scripts/demo-kind.sh forward

demo-down:
	./scripts/demo-kind.sh down

.PHONY: e2e-release
e2e-release:
	RELEASE_VERSION="$(RELEASE_VERSION)" \
	bash ./scripts/e2e-release.sh
