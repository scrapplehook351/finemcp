GO ?= go
GOFMT ?= gofmt "-s"
GOFILES := $(shell find . -name "*.go" -not -path "./.git/*")
PACKAGES ?= $(shell $(GO) list ./...)
VETPACKAGES ?= $(shell $(GO) list ./... | grep -v /examples/)
# Only packages that contain test files — ensures consistent coverage totals
# across platforms (Linux includes testless packages at 0%; macOS excludes them).
TESTPACKAGES ?= $(shell $(GO) list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./...)
COVERAGE_MIN ?= 85
TIMEOUT ?= 120s

GOLANGCI_LINT := GOTOOLCHAIN=go1.25.10 $(GO) run github.com/golangci/golangci-lint/cmd/golangci-lint@latest
GOVULNCHECK   := GOTOOLCHAIN=go1.25.10 $(GO) run golang.org/x/vuln/cmd/govulncheck@latest
GOSEC         := $(GO) run github.com/securego/gosec/v2/cmd/gosec@latest
MISSPELL      := $(GO) run github.com/client9/misspell/cmd/misspell@latest

# ─── Build ───────────────────────────────────────────────────────────

.PHONY: build
# Compile all packages.
build:
	$(GO) build ./...

# ─── Test ────────────────────────────────────────────────────────────

.PHONY: test
# Run all tests with race detector.
test:
	$(GO) test -race -count=1 -timeout $(TIMEOUT) ./...

.PHONY: test-verbose
# Run all tests with verbose output.
test-verbose:
	$(GO) test -race -count=1 -timeout $(TIMEOUT) -v ./...

.PHONY: test-short
# Run tests in short mode (skip long-running tests).
test-short:
	$(GO) test -race -count=1 -timeout 60s -short ./...

# ─── Coverage ────────────────────────────────────────────────────────

.PHONY: coverage
# Show test coverage summary.
coverage:
	$(GO) test -coverprofile=coverage.out -covermode=atomic ./...
	$(GO) tool cover -func=coverage.out
	@rm -f coverage.out

.PHONY: coverage-html
# Open coverage report in browser.
coverage-html:
	$(GO) test -coverprofile=coverage.out -covermode=atomic ./...
	$(GO) tool cover -html=coverage.out
	@rm -f coverage.out

.PHONY: coverage-check
# Fail if total coverage drops below threshold.
coverage-check:
	@$(GO) test -coverprofile=coverage.out -covermode=atomic $(TESTPACKAGES) 2>/dev/null; \
	total=$$($(GO) tool cover -func=coverage.out | grep total | awk '{print $$3}' | sed 's/%//'); \
	rm -f coverage.out; \
	threshold=$(COVERAGE_MIN); \
	if [ $$(echo "$$total < $$threshold" | bc -l) -eq 1 ]; then \
		echo "FAIL: coverage $$total% is below minimum $$threshold%"; \
		exit 1; \
	else \
		echo "OK: coverage $$total% >= $$threshold%"; \
	fi

# ─── Format ──────────────────────────────────────────────────────────

.PHONY: fmt
# Format all Go source files.
fmt:
	$(GOFMT) -w $(GOFILES)

.PHONY: fmt-check
# Check formatting (fails if files need formatting).
fmt-check:
	@diff=$$($(GOFMT) -d $(GOFILES)); \
	if [ -n "$$diff" ]; then \
		echo "Please run 'make fmt' and commit the result:"; \
		echo "$${diff}"; \
		exit 1; \
	fi

# ─── Lint ────────────────────────────────────────────────────────────

.PHONY: vet
# Examine packages and report suspicious constructs.
vet:
	$(GO) vet $(VETPACKAGES)

.PHONY: lint
# Run golangci-lint with project config.
lint:
	$(GOLANGCI_LINT) run ./...

.PHONY: lint-fix
# Run golangci-lint and automatically fix issues.
lint-fix:
	$(GOLANGCI_LINT) run --fix ./...

# ─── Security ────────────────────────────────────────────────────────

.PHONY: vuln
# Scan dependencies for known vulnerabilities.
vuln:
	$(GOVULNCHECK) ./...

.PHONY: sec
# Run static security analysis.
sec:
	$(GOSEC) -quiet -exclude-dir=examples ./...

# ─── Misspell ────────────────────────────────────────────────────────

.PHONY: misspell
# Correct commonly misspelled words in source files.
misspell:
	$(MISSPELL) -w $(GOFILES)

.PHONY: misspell-check
# Check for misspellings (fails if found).
misspell-check:
	$(MISSPELL) -error $(GOFILES)

# ─── CI ──────────────────────────────────────────────────────────────

.PHONY: ci
# Run the full CI pipeline locally (same checks as GitHub Actions).
ci: build vet fmt-check lint test coverage-check vuln
	@echo ""
	@echo "✓ All CI checks passed."

# ─── Tools ───────────────────────────────────────────────────────────

.PHONY: tools
# Pre-download all development tool modules.
tools:
	@echo "Caching tool modules..."
	@$(GOLANGCI_LINT) version > /dev/null 2>&1
	@$(GOVULNCHECK) -version > /dev/null 2>&1 || true
	@$(GOSEC) --version > /dev/null 2>&1 || true
	@$(MISSPELL) < /dev/null > /dev/null 2>&1 || true
	@echo "Done."

# ─── Benchmarks ──────────────────────────────────────────────────────

.PHONY: bench
# Run all benchmarks.
bench:
	$(GO) test -bench=. -benchmem -run=^$$ ./client/... 2>&1 | tee bench.out

# ─── Release ─────────────────────────────────────────────────────────

GORELEASER := $(GO) run github.com/goreleaser/goreleaser@latest

.PHONY: release-snapshot
# Test the release process locally without publishing.
release-snapshot:
	$(GORELEASER) release --snapshot --clean --skip=publish

.PHONY: release-check
# Verify release configuration is valid.
release-check:
	$(GORELEASER) check

.PHONY: release-dry
# Show what would be released without actually releasing.
release-dry:
	$(GORELEASER) release --skip=publish --clean

.PHONY: release-notes
# Generate release notes for the current version.
release-notes:
	@echo "Generating release notes..."
	@git log $$(git describe --tags --abbrev=0 2>/dev/null || echo "")..HEAD --oneline --no-merges

.PHONY: tag
# Create and push a new version tag (usage: make tag VERSION=v1.0.0).
tag:
	@if [ -z "$(VERSION)" ]; then \
		echo "Error: VERSION is required. Usage: make tag VERSION=v1.0.0"; \
		exit 1; \
	fi
	@echo "Creating tag $(VERSION)..."
	@git tag -a $(VERSION) -m "Release $(VERSION)"
	@echo "Pushing tag $(VERSION)..."
	@git push origin $(VERSION)
	@echo ""
	@echo "✓ Tag $(VERSION) created and pushed."
	@echo "  GitHub Actions will now build and publish the release."
	@echo "  Monitor: https://github.com/finemcp/finemcp/actions"

# ─── Clean ───────────────────────────────────────────────────────────

.PHONY: clean
# Remove build artifacts and caches.
clean:
	@rm -f coverage.out profile.out
	$(GO) clean ./...
	$(GO) clean -testcache

# ─── Dev Setup ────────────────────────────────────────────────────────

HOOKS := pre-commit commit-msg pre-push

.PHONY: setup
# Install git hooks and set up the local dev environment.
setup:
	@echo "==> Installing git hooks…"
	@mkdir -p .git/hooks
	@for hook in $(HOOKS); do \
		cp .dev/hooks/$$hook .git/hooks/$$hook; \
		chmod +x .git/hooks/$$hook; \
		echo "    ✓ $$hook"; \
	done
	@echo "==> Caching tool modules…"
	@$(GOLANGCI_LINT) version > /dev/null 2>&1
	@$(GOVULNCHECK) -version > /dev/null 2>&1 || true
	@$(GOSEC) --version > /dev/null 2>&1 || true
	@$(MISSPELL) < /dev/null > /dev/null 2>&1 || true
	@echo "==> Downloading module dependencies…"
	@$(GO) mod download
	@echo ""
	@echo "✓ Dev environment ready. Run 'make verify' to confirm."

.PHONY: verify
# Verify the local dev environment is correctly set up.
verify:
	@echo "==> Checking Go toolchain…"
	@$(GO) version
	@echo ""
	@echo "==> Checking git hooks…"
	@all_ok=true; \
	for hook in $(HOOKS); do \
		if [ -x .git/hooks/$$hook ]; then \
			canonical_ver=$$(grep -m1 "# finemcp.*hook v" .dev/hooks/$$hook 2>/dev/null | sed -E 's/.*v([0-9.]+).*/\1/' || echo "unknown"); \
			installed_ver=$$(grep -m1 "# finemcp.*hook v" .git/hooks/$$hook 2>/dev/null | sed -E 's/.*v([0-9.]+).*/\1/' || echo "unknown"); \
			if [ "$$canonical_ver" = "$$installed_ver" ]; then \
				echo "    ✓ $$hook (v$$installed_ver)"; \
			else \
				echo "    ⚠ $$hook (v$$installed_ver, expected v$$canonical_ver — run 'make setup' to update)"; \
				all_ok=false; \
			fi; \
		else \
			echo "    ✗ $$hook missing — run 'make setup'"; \
			all_ok=false; \
		fi; \
	done; \
	echo ""; \
	echo "==> Checking tools…"; \
	$(GOLANGCI_LINT) version > /dev/null 2>&1 && echo "    ✓ golangci-lint" || { echo "    ✗ golangci-lint (run 'make setup')"; all_ok=false; }; \
	$(GOVULNCHECK) -version > /dev/null 2>&1 && echo "    ✓ govulncheck"   || { echo "    ✗ govulncheck (run 'make setup')"; all_ok=false; }; \
	$(GOSEC) --version > /dev/null 2>&1       && echo "    ✓ gosec"         || { echo "    ✗ gosec (run 'make setup')"; all_ok=false; }; \
	$(MISSPELL) < /dev/null > /dev/null 2>&1  && echo "    ✓ misspell"      || { echo "    ✗ misspell (run 'make setup')"; all_ok=false; }; \
	echo ""; \
	echo "==> Building project…"; \
	$(GO) build ./... && echo "    ✓ build OK" || { echo "    ✗ build failed"; all_ok=false; }; \
	echo ""; \
	echo "==> Running short tests…"; \
	$(GO) test -race -count=1 -timeout 60s -short ./... > /dev/null 2>&1 \
		&& echo "    ✓ tests OK" \
		|| { echo "    ✗ tests failed"; all_ok=false; }; \
	echo ""; \
	if $$all_ok; then \
		echo "✓ Dev environment verified — everything looks good."; \
	else \
		echo "✗ Some checks failed. Run 'make setup' and try again."; \
		exit 1; \
	fi

# ─── Roadmap ─────────────────────────────────────────────────────────

# .PHONY: roadmap
# # Regenerate the version roadmap table (deprecated - moved to website).
# roadmap:
# 	@./scripts/update-roadmap.sh

# .PHONY: roadmap-check
# # Check that the version roadmap table is up to date (for CI - deprecated).
# roadmap-check:
# 	@./scripts/update-roadmap.sh --check

# ─── Help ────────────────────────────────────────────────────────────

.PHONY: help
# Show this help.
help:
	@echo ''
	@echo 'Usage:'
	@echo '  make [target]'
	@echo ''
	@echo 'Targets:'
	@awk '/^[a-zA-Z\-\_0-9]+:/ { \
		helpMessage = match(lastLine, /^# (.*)/); \
		if (helpMessage) { \
			helpCommand = substr($$1, 0, index($$1, ":")-1); \
			helpMessage = substr(lastLine, RSTART + 2, RLENGTH); \
			printf "  \033[36m%-20s\033[0m %s\n", helpCommand, helpMessage; \
		} \
	} \
	{ lastLine = $$0 }' $(MAKEFILE_LIST)
	@echo ''

.DEFAULT_GOAL := help