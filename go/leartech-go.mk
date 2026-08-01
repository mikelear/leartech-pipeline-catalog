# leartech-go.mk — canonical Go build/test/lint targets for leartech services.
#
# SINGLE source of truth for what the Tekton go-lint / go-test tasks run and
# what a developer runs on their laptop. If the two ever drift, this file is
# wrong. The Tekton tasks (tasks/go-lint/pullrequest.yaml and
# tasks/go-test/pullrequest.yaml) invoke the same shell logic as the recipes
# below; the parity test at test/go/parity_test.sh asserts the merged linter
# set produced by `lint-config` here matches the one the task produces.
#
# Two usage shapes, both supported:
#
#   1) One-shot from a consumer repo (no include):
#        curl -fsSL https://raw.githubusercontent.com/mikelear/leartech-pipeline-catalog/main/go/leartech-go.mk -o leartech-go.mk
#        make -f leartech-go.mk lint
#        make -f leartech-go.mk test-coverage
#
#   2) `include`d from a repo-local Makefile:
#        # Makefile
#        include leartech-go.mk
#        # add repo-specific targets below
#
# Consumer-tunable variables (all `?=` so a repo can override before include):
#
#   GOLANGCI_VERSION       ONE canonical golangci-lint version — bump here,
#                          the tasks step image gets bumped to match in the
#                          same PR. See README "Canonical Go toolchain versions".
#   GOLANGCI_BASE_URL      Where to fetch the base config from at CI time.
#                          Defaults to the raw file in leartech-pipeline-catalog@main.
#   GOLANGCI_BASE_FILE     Optional local path to the base config. When set and
#                          the file exists, used INSTEAD of curl'ing the URL —
#                          lets leartech-pipeline-catalog itself dogfood the mk
#                          against ./go/.golangci.base.yml without a round-trip
#                          to raw.githubusercontent.
#   GOLANGCI_MERGED        Output path for the merged config (default
#                          .golangci.merged.yml in the repo root).
#   COVERAGE_SCOPE         `go test -coverpkg` scope (default ./internal/...).
#   COVERAGE_THRESHOLD     Minimum acceptable total coverage % (default 60.0).
#   COVERAGE_DELTA_TOLERANCE
#                          Max allowed drop vs base branch coverage in
#                          percentage points (default 0.5, absorbs noise).
#   PULL_BASE_REF          Base branch for delta-vs-base coverage check.
#                          Auto-detected in Tekton via Lighthouse; defaults
#                          to `main` locally.
#   REPO_OWNER / REPO_NAME For the delta baseline clone. Tekton sets these
#                          automatically; locally they are inferred from
#                          `git remote get-url origin` when unset. If they
#                          can't be inferred (offline, no remote) the delta
#                          check no-ops cleanly — pass, not fail.

# ── Shell (bash-required) ─────────────────────────────────────────────────
#
# Recipes below use bash-only features — `set -o pipefail` and `<(...)`
# process substitution inside strip_generated. GNU make defaults SHELL to
# /bin/sh which on Debian/Alpine is dash/ash and does NOT support either.
# Without this override every consumer curl'ing this file and running
# `make -f leartech-go.mk test-coverage` in a debian-slim / golang:1.26 /
# alpine base image hits `Syntax error: "(" unexpected` at line 11 of the
# test-coverage recipe. Setting SHELL here makes the mk self-contained —
# no caller needs to know or pass SHELL=/bin/bash. Bash is present in
# every image the Tekton go-lint/go-test tasks use (golangci-lint,
# golang:1.26, alpine after `apk add bash`).
SHELL := /bin/bash

# ── Canonical toolchain versions ─────────────────────────────────────────
GOLANGCI_VERSION ?= 2.12.2

# ── Config paths ─────────────────────────────────────────────────────────
GOLANGCI_BASE_URL ?= https://raw.githubusercontent.com/mikelear/leartech-pipeline-catalog/main/go/.golangci.base.yml
GOLANGCI_BASE_FILE ?=
GOLANGCI_MERGED ?= .golangci.merged.yml

# ── Coverage knobs (mirror tasks/go-test/pullrequest.yaml defaults) ──────
COVERAGE_SCOPE ?= ./internal/...
COVERAGE_THRESHOLD ?= 60.0
COVERAGE_DELTA_TOLERANCE ?= 0.5

# ── Delta baseline knobs ─────────────────────────────────────────────────
PULL_BASE_REF ?= main

# .DEFAULT_GOAL so `make -f leartech-go.mk` (no target) prints help.
.DEFAULT_GOAL := help

.PHONY: help lint-config lint vet tidy-check test test-coverage build vuln pre-push

help: ## Print available targets
	@echo ""
	@echo "  leartech-go.mk — Go build/test/lint (canonical)"
	@echo "  ================================================"
	@echo ""
	@echo "  Usage:  make -f leartech-go.mk <target>"
	@echo ""
	@echo "  Targets:"
	@echo "    lint-config     Fetch base config + yq-merge with local .golangci.yml → $(GOLANGCI_MERGED)"
	@echo "    lint            Run golangci-lint (depends on lint-config)"
	@echo "    vet             go vet ./..."
	@echo "    tidy-check      Verify go.mod / go.sum are tidy"
	@echo "    test            go test ./... -race (no coverage)"
	@echo "    test-coverage   Race + coverage, enforce floor + delta-vs-base"
	@echo "    build           go build ./..."
	@echo "    vuln            govulncheck ./..."
	@echo "    pre-push        vet tidy-check build test-coverage lint vuln"
	@echo ""
	@echo "  Canonical versions:"
	@echo "    golangci-lint    $(GOLANGCI_VERSION)"
	@echo ""
	@echo "  Coverage knobs (env overridable):"
	@echo "    COVERAGE_SCOPE            $(COVERAGE_SCOPE)"
	@echo "    COVERAGE_THRESHOLD        $(COVERAGE_THRESHOLD)%"
	@echo "    COVERAGE_DELTA_TOLERANCE  $(COVERAGE_DELTA_TOLERANCE)%"
	@echo ""

# ── lint-config: mirror of the yq-merge block in tasks/go-lint/pullrequest.yaml ──
#
# Byte-for-byte identical merge semantics: yq `. as $item ireduce ({}; . *+ $item)`
# treats the LAST document as the winner, so the local file wins on any conflict.
# When no local .golangci.yml exists, base is used as-is.
lint-config: ## Produce $(GOLANGCI_MERGED) from base config (curled or local) merged with ./.golangci.yml
	@set -eu; \
	if ! command -v yq >/dev/null 2>&1; then \
	  echo "==> yq not found on PATH"; \
	  echo "    Install: https://github.com/mikefarah/yq (v4)"; \
	  exit 1; \
	fi; \
	base=/tmp/leartech-golangci.base.yml; \
	if [ -n "$(GOLANGCI_BASE_FILE)" ] && [ -f "$(GOLANGCI_BASE_FILE)" ]; then \
	  echo "==> using local base $(GOLANGCI_BASE_FILE)"; \
	  cp "$(GOLANGCI_BASE_FILE)" "$$base"; \
	else \
	  echo "==> fetching base config from $(GOLANGCI_BASE_URL)"; \
	  curl -fsSL -o "$$base" "$(GOLANGCI_BASE_URL)"; \
	fi; \
	echo "==> base config ($$(wc -l < $$base) lines)"; \
	if [ -f .golangci.yml ]; then \
	  echo "==> merging local .golangci.yml ($$(wc -l < .golangci.yml) lines) onto base"; \
	  yq eval-all '. as $$item ireduce ({}; . *+ $$item)' "$$base" .golangci.yml > "$(GOLANGCI_MERGED)"; \
	else \
	  echo "==> no local .golangci.yml; using base as-is"; \
	  cp "$$base" "$(GOLANGCI_MERGED)"; \
	fi; \
	echo "==> merged config → $(GOLANGCI_MERGED) ($$(wc -l < $(GOLANGCI_MERGED)) lines)"

# ── lint: mirror of the golangci-lint invocation in tasks/go-lint/pullrequest.yaml ──
#
# --timeout 15m matches the task; see task comment (Azure builder nodes hit
# 10m on cold-cache package-load). Locally the timeout is generous, not
# tight — matching the CI value keeps the two runs comparable.
lint: lint-config ## Run golangci-lint against the merged config
	@set -eu; \
	if ! command -v golangci-lint >/dev/null 2>&1; then \
	  echo "==> golangci-lint not found on PATH"; \
	  echo "    Install v$(GOLANGCI_VERSION): https://golangci-lint.run/welcome/install/"; \
	  exit 1; \
	fi; \
	echo "==> go mod download"; \
	go mod download; \
	echo "==> golangci-lint run -v --timeout 15m --config $(GOLANGCI_MERGED) ./..."; \
	golangci-lint run -v --timeout 15m --config $(GOLANGCI_MERGED) ./...; \
	echo "==> lint complete"

vet: ## go vet ./...
	go vet ./...

# tidy-check: fails if `go mod tidy` would change go.mod / go.sum. Uses the
# `-diff` mode when available (Go 1.23+); falls back to a copy-and-compare
# otherwise.
tidy-check: ## Verify go.mod / go.sum are tidy
	@set -eu; \
	if go help mod | grep -q '^\s*tidy'; then \
	  if go mod tidy -diff >/tmp/leartech-tidy.diff 2>&1; then \
	    if [ -s /tmp/leartech-tidy.diff ]; then \
	      echo "==> go.mod/go.sum are NOT tidy; run 'go mod tidy':"; \
	      cat /tmp/leartech-tidy.diff; \
	      exit 1; \
	    fi; \
	    echo "==> go.mod/go.sum tidy"; \
	    exit 0; \
	  fi; \
	fi; \
	echo "==> falling back to copy-and-compare tidy check"; \
	cp go.mod /tmp/leartech-go.mod.bak; \
	cp go.sum /tmp/leartech-go.sum.bak 2>/dev/null || true; \
	go mod tidy; \
	if ! diff -q go.mod /tmp/leartech-go.mod.bak >/dev/null 2>&1 \
	   || { [ -f /tmp/leartech-go.sum.bak ] && ! diff -q go.sum /tmp/leartech-go.sum.bak >/dev/null 2>&1; }; then \
	  echo "==> go.mod/go.sum are NOT tidy; commit the tidy result"; \
	  mv /tmp/leartech-go.mod.bak go.mod; \
	  [ -f /tmp/leartech-go.sum.bak ] && mv /tmp/leartech-go.sum.bak go.sum || true; \
	  exit 1; \
	fi; \
	echo "==> go.mod/go.sum tidy"

test: ## go test ./... -race (no coverage)
	go test ./... -v -count=1 -race

build: ## go build ./...
	go build ./...

# vuln: govulncheck ./... — installed on-demand if missing.
vuln: ## Run govulncheck ./...
	@set -eu; \
	if ! command -v govulncheck >/dev/null 2>&1; then \
	  echo "==> govulncheck not found; installing..."; \
	  go install golang.org/x/vuln/cmd/govulncheck@latest; \
	  export PATH="$$PATH:$$(go env GOPATH)/bin"; \
	fi; \
	govulncheck ./...

# ── test-coverage: mirror of the go-test script (minus PR-comment posting) ──
#
# Byte-for-byte identical `go test` invocation, coverage stripping, floor
# check, and delta-vs-base logic. The PR-comment posting stays in the Tekton
# task (needs GIT_TOKEN + PULL_NUMBER; irrelevant locally).
#
# The base-clone / delta section is GUARDED so that local runs with no
# origin remote / no network / no matching base branch pass cleanly with a
# "delta check unavailable" line — never fails the local run for infra
# reasons. Task behaviour is preserved in CI because Tekton always sets
# REPO_OWNER / REPO_NAME / PULL_BASE_REF and has network to github.com.
test-coverage: ## Race + coverage, enforce floor + delta-vs-base
	@set -eo pipefail; \
	SCOPE="$(COVERAGE_SCOPE)"; \
	THRESHOLD="$(COVERAGE_THRESHOLD)"; \
	DELTA_TOL="$(COVERAGE_DELTA_TOLERANCE)"; \
	echo "=== go test -race -coverprofile (scope=$$SCOPE, threshold=$$THRESHOLD%) ==="; \
	go test ./... -v -count=1 -race -coverpkg="$$SCOPE" -coverprofile=cover.out; \
	strip_generated() { \
	  prof="$$1"; root="$${2:-.}"; \
	  gen=$$(cd "$$root" && { grep -rlE '^// Code generated .* DO NOT EDIT\.$$' --include='*.go' . 2>/dev/null || true; } | sed 's#^\./##'); \
	  [ -z "$$gen" ] && return 0; \
	  grep -vFf <(printf '%s\n' $$gen) "$$prof" > "$${prof}.f" 2>/dev/null || true; \
	  if [ -s "$${prof}.f" ]; then mv "$${prof}.f" "$$prof"; else rm -f "$${prof}.f"; fi; \
	}; \
	strip_generated cover.out .; \
	echo "(generated files excluded from coverage)"; \
	echo; \
	echo "=== per-function coverage ==="; \
	go tool cover -func=cover.out; \
	TOTAL=$$(go tool cover -func=cover.out | awk '/^total:/ {print $$3}' | sed 's/%//'); \
	echo; \
	echo "=== coverage summary ==="; \
	echo "total=$${TOTAL}%  threshold=$${THRESHOLD}%"; \
	STATUS="pass"; STATUS_REASON=""; \
	if awk -v t="$$TOTAL" -v th="$$THRESHOLD" 'BEGIN { exit !(t < th) }'; then \
	  STATUS="fail"; STATUS_REASON="below floor $${THRESHOLD}%"; \
	fi; \
	BASE_REF="$${PULL_BASE_REF:-$(PULL_BASE_REF)}"; \
	BASE_TOTAL=""; BASE_DELTA=""; BASE_STATUS=""; \
	echo; \
	echo "=== delta-coverage check vs origin/$${BASE_REF} ==="; \
	OWNER="$${REPO_OWNER:-}"; NAME="$${REPO_NAME:-}"; \
	if [ -z "$$OWNER" ] || [ -z "$$NAME" ]; then \
	  if remote_url=$$(git remote get-url origin 2>/dev/null); then \
	    slug=$$(printf '%s' "$$remote_url" | sed -E 's#^(https?://[^/]+/|git@[^:]+:)##; s#\.git$$##'); \
	    OWNER="$${OWNER:-$$(printf '%s' "$$slug" | cut -d/ -f1)}"; \
	    NAME="$${NAME:-$$(printf '%s' "$$slug" | cut -d/ -f2)}"; \
	  fi; \
	fi; \
	if [ -n "$$OWNER" ] && [ -n "$$NAME" ]; then \
	  BASE_CLONE_DIR=$$(mktemp -d /tmp/go-test-base-XXXXXX); \
	  REPO_URL="https://github.com/$${OWNER}/$${NAME}.git"; \
	  if git clone --depth=1 --branch="$${BASE_REF}" --quiet "$$REPO_URL" "$$BASE_CLONE_DIR" 2>/dev/null; then \
	    if (cd "$$BASE_CLONE_DIR" && go test ./... -count=1 -coverpkg="$$SCOPE" -coverprofile=cover-base.out) >/tmp/base-test.log 2>&1; then \
	      strip_generated "$${BASE_CLONE_DIR}/cover-base.out" "$$BASE_CLONE_DIR"; \
	      BASE_TOTAL=$$(go tool cover -func="$${BASE_CLONE_DIR}/cover-base.out" 2>/dev/null | awk '/^total:/ {print $$3}' | sed 's/%//'); \
	    fi; \
	    rm -rf "$$BASE_CLONE_DIR"; \
	  fi; \
	else \
	  echo "REPO_OWNER/REPO_NAME unset and origin remote absent — skipping delta check"; \
	fi; \
	if [ -n "$$BASE_TOTAL" ]; then \
	  BASE_DELTA=$$(awk -v pr="$$TOTAL" -v base="$$BASE_TOTAL" 'BEGIN {printf "%.2f", pr - base}'); \
	  echo "base=$${BASE_TOTAL}%  pr=$${TOTAL}%  delta=$${BASE_DELTA}%  tolerance=-$${DELTA_TOL}%"; \
	  if awk -v d="$$BASE_DELTA" -v tol="$$DELTA_TOL" 'BEGIN { exit !(d < -tol) }'; then \
	    STATUS="fail"; \
	    STATUS_REASON="$${STATUS_REASON:+$$STATUS_REASON; }coverage dropped $${BASE_DELTA}% vs origin/$${BASE_REF} (tolerance -$${DELTA_TOL}%)"; \
	    BASE_STATUS="REGRESSED"; \
	  else \
	    BASE_STATUS="OK"; \
	  fi; \
	else \
	  echo "base-branch coverage unavailable (new repo, base test failure, fetch denied, or offline); skipping delta check"; \
	  BASE_STATUS="UNAVAILABLE"; \
	fi; \
	if [ "$$STATUS" = "fail" ]; then \
	  echo "FAIL: $$STATUS_REASON"; \
	  exit 1; \
	fi; \
	echo "PASS: coverage $${TOTAL}% meets threshold $${THRESHOLD}% (delta=$${BASE_DELTA:-n/a}% vs $${BASE_REF})"

# pre-push: what you should run before `git push`. Order chosen so cheap
# checks fail fast:  vet → tidy-check → build → test-coverage → lint → vuln.
pre-push: vet tidy-check build test-coverage lint vuln ## Full pre-push gate (vet tidy-check build test-coverage lint vuln)
	@echo "==> pre-push gate: all checks passed"
