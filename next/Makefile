SHELL := /bin/sh

GO ?= go
NODE ?= node
PNPM ?= pnpm
PYTHON ?= python3

GO_VERSION := go1.27.1
NODE_VERSION := v24.21.0
PNPM_VERSION := 12.4.2
DIST := $(CURDIR)/dist

.PHONY: bootstrap generate generate-check build test lint check clean verify-tools

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

test: verify-tools generate-check
	$(GO) test ./backend/...
	$(PNPM) run test

lint: verify-tools generate-check
	@test -z "$$($(GO)fmt -l backend)" || \
		{ echo "Go files require gofmt"; $(GO)fmt -l backend; exit 1; }
	$(GO) vet ./backend/...
	$(PNPM) run lint

check: lint test build

clean:
	rm -rf $(DIST)
