SHELL := /bin/sh

GO ?= go
NODE ?= node
PNPM ?= pnpm
PYTHON ?= python3

GO_VERSION := go1.27.1
NODE_VERSION := v24.21.0
PNPM_VERSION := 12.4.2
GOVULNCHECK_VERSION := v1.8.0
DIST := $(CURDIR)/dist
BROWSER_CACHE := $(CURDIR)/.cache/ms-playwright

.PHONY: bootstrap generate generate-check build test lint check clean verify-tools \
	contract-check workspace-check quality-gate-tests license-check browser-install \
	browser-smoke advisory artifact artifact-check

verify-tools:
	@test "$$($(GO) version | awk '{print $$3}')" = "$(GO_VERSION)" || \
		{ echo "expected $(GO_VERSION)"; exit 1; }
	@test "$$($(NODE) --version)" = "$(NODE_VERSION)" || \
		{ echo "expected Node $(NODE_VERSION)"; exit 1; }
	@test "$$($(PNPM) --version)" = "$(PNPM_VERSION)" || \
		{ echo "expected pnpm $(PNPM_VERSION)"; exit 1; }

bootstrap: verify-tools
	$(PNPM) install --frozen-lockfile
	$(GO) work sync

generate:
	$(PYTHON) tools/generate_sdk.py

generate-check:
	$(PYTHON) tools/generate_sdk.py --check

build: verify-tools generate-check
	@mkdir -p $(DIST)/backend $(DIST)/contracts $(DIST)/sdk-package
	$(GO) build -trimpath -o $(DIST)/backend/teddycloud-next ./backend/cmd/teddycloud-next
	cp contracts/openapi.json $(DIST)/contracts/openapi.json
	$(PNPM) run build
	cp sdk/typescript/package.publish.json $(DIST)/sdk/package.json
	cp sdk/typescript/README.md $(DIST)/sdk/README.md
	cd $(DIST)/sdk && $(PNPM) pack --pack-destination ../sdk-package

contract-check:
	$(PYTHON) tools/validate_contract.py contracts/openapi.json

workspace-check:
	$(PYTHON) tools/check_workspace_boundaries.py

quality-gate-tests:
	$(PYTHON) -m unittest discover -s tests -p 'test_*.py' -v

license-check: verify-tools
	@mkdir -p $(DIST)/reports
	$(PNPM) licenses list --json > $(DIST)/reports/js-licenses.json
	$(PYTHON) tools/check_licenses.py $(DIST)/reports/js-licenses.json

test: verify-tools generate-check
	$(GO) test ./backend/...
	$(PNPM) run test

lint: verify-tools generate-check
	@test -z "$$($(GO)fmt -l backend)" || \
		{ echo "Go files require gofmt"; $(GO)fmt -l backend; exit 1; }
	$(GO) vet ./backend/...
	$(PNPM) run lint

check: contract-check quality-gate-tests workspace-check lint test build license-check

browser-install: verify-tools
	PLAYWRIGHT_BROWSERS_PATH=$(BROWSER_CACHE) $(PNPM) exec playwright install chromium

browser-smoke: verify-tools build
	PLAYWRIGHT_BROWSERS_PATH=$(BROWSER_CACHE) $(PNPM) run browser:test

advisory: verify-tools
	cd backend && $(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...
	$(PNPM) audit --audit-level high

artifact: build
	$(PYTHON) tools/artifact_manifest.py create

artifact-check:
	$(PYTHON) tools/artifact_manifest.py verify

clean:
	rm -rf $(DIST)
