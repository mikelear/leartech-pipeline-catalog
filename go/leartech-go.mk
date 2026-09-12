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
#   GOLANGCI_VERSION       golangci-lint version this mk expects on PATH.
#   GOLANGCI_BASE_URL      Where to fetch the base config from at CI time.
#                          Defaults to the raw file in leartech-pipeline-catalog@main.
#   GOLANGCI_BASE_FILE     Optional local path to the base config. When set and
#                          the file exists, used INSTEAD of curl'ing the URL —
#                          lets leartech-pipeline-catalog itself dogfood the mk
#                          against ./go/.golangci.base.yml without a round-trip
#                          to raw.githubusercontent.
#   GOLANGCI_MERGED        Output path for the merged config (default
#                          .golangci.merged.yml in the repo root).
#   AUTHCONF_URL           Where to fetch the auth-conformance checker from.
#                          Defaults to the raw file in leartech-pipeline-catalog@main.
#   AUTHCONF_FILE          Optional local path to the checker source. When set
#                          and present, used INSTEAD of curl'ing — lets the
#                          catalog dogfood ./go/authconformance/main.go.
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

# ── Canonical toolchain versions ─────────────────────────────────────────
GOLANGCI_VERSION ?= 2.13.2

# ── Config paths ─────────────────────────────────────────────────────────
GOLANGCI_BASE_URL ?= https://raw.githubusercontent.com/mikelear/leartech-pipeline-catalog/main/go/.golangci.base.yml
GOLANGCI_BASE_FILE ?=
GOLANGCI_MERGED ?= .golangci.merged.yml

# auth-conformance: the estate auth standard as a gate. Same curl-or-local
# shape as GOLANGCI_BASE_*, so the catalog can dogfood it against its own copy.
AUTHCONF_URL ?= https://raw.githubusercontent.com/mikelear/leartech-pipeline-catalog/main/go/authconformance/main.go
AUTHCONF_FILE ?=

# ── Coverage knobs (mirror tasks/go-test/pullrequest.yaml defaults) ──────
#
# The *_DEFAULT copies below are the SAME literals, held as non-overridable
# `:=` so `test-coverage` can detect when a caller has changed a knob and warn
# that the run no longer matches CI. Keep each pair in lockstep — the parity
# test asserts they agree, so a drift here fails the catalog's own gate.
#
# WHY THIS EXISTS: on 2026-08-15 an agent correctly curled this golden mk and
# invoked it directly (as pre-push-validation mandates) but ran it with
# COVERAGE_DELTA_TOLERANCE=5.0 — ten times the pinned 0.5. Its pre-push check
# went green, CI failed the delta gate, and go-test burned three cycles before
# anyone noticed the local run had never been equivalent. Overriding the pinned
# values silently converts "verified against the golden mk" into "verified
# against something else that looks like it".
COVERAGE_SCOPE ?= ./internal/...
COVERAGE_THRESHOLD ?= 60.0
COVERAGE_DELTA_TOLERANCE ?= 0.5

COVERAGE_SCOPE_DEFAULT := ./internal/...
COVERAGE_THRESHOLD_DEFAULT := 60.0
COVERAGE_DELTA_TOLERANCE_DEFAULT := 0.5

# ── Delta baseline knobs ─────────────────────────────────────────────────
PULL_BASE_REF ?= main

# ── File-size knobs (accumulation guard) ─────────────────────────────────
# golangci-lint has NO file-length linter — it isn't a category the tool
# ships. Every complexity linter it does ship (cyclop / gocyclo / nestif /
# funlen) is per-FUNCTION, so a 3,000-line file made of forty tidy 70-line
# functions passes all of them cleanly. ai-review can't see it either: that
# path reviews the PR *diff*, never the file being modified, so a PR adding
# 150 lines to a 3,000-line file looks like 150 lines.
#
# File size is therefore an emergent property that NO single PR introduces
# and NO existing gate measures. This target closes that gap deterministically
# — no LLM, no labels, no context budget.
#
# WARN-ONLY by default: FILE_SIZE_FAIL=0 means never exit non-zero. Set it to
# a line count to make the check blocking for a repo (or estate-wide, once the
# warn output has been reviewed). 1200 is the suggested first blocking value —
# see the target's comment for why.
FILE_SIZE_WARN ?= 800
FILE_SIZE_FAIL ?= 0

# .DEFAULT_GOAL so `make -f leartech-go.mk` (no target) prints help.
.DEFAULT_GOAL := help

.PHONY: auth-conformance auth-standard help lint-config lint file-size vet tidy-check test test-coverage build vuln pre-push

help: ## Print available targets
	@echo ""
	@echo "  leartech-go.mk — Go build/test/lint (canonical)"
	@echo "  ================================================"
	@echo ""
	@echo "  Usage:  make -f leartech-go.mk <target>"
	@echo ""
	@echo "  Targets:"
	@echo "    lint-config     Fetch base config + yq-merge with local .golangci.yml → $(GOLANGCI_MERGED)"
	@echo "    auth-conformance Enforce the estate auth standard (code + chart)"
	@echo "    auth-standard   Print the auth standard + rationale (no checks run)"
	@echo "    lint            Run golangci-lint (depends on lint-config, file-size)"
	@echo "    file-size       Report hand-written .go files over the warn threshold"
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
	@echo "  File-size knobs (env overridable):"
	@echo "    FILE_SIZE_WARN            $(FILE_SIZE_WARN) lines (report above this)"
	@echo "    FILE_SIZE_FAIL            $(FILE_SIZE_FAIL) (0 = warn only, never blocks)"
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


# ── auth-conformance: the estate auth standard, enforced ──────────────────────
#
# Runs on every PR (via `lint`) and every release for any repo that is BOTH a
# deployed service (a chart with a Chart.yaml) AND an auth consumer (imports
# go-common's auth package). Everything else is skipped, loudly.
#
# It fails a build when a service departs from the standard: go-common below
# the floor, the dual-role ServiceClient used for an inbound-only service, a
# chart missing LEARTECH_AUTH_ISSUER / LEARTECH_AUTH_AUDIENCE or carrying the
# old envconfig-era names, or ANY flag that can turn auth off.
#
# A repo with a genuine exception declares it in `.authconformance` with a
# mandatory reason — see the checker's godoc. Exceptions arrive in a diff with
# an argument attached, where a reviewer can disagree with them.
#
# Run in a throwaway module: the checker is stdlib-only precisely so this needs
# no network fetch beyond the source itself and adds nothing to the consumer's
# go.mod.
auth-standard: ## Print the estate auth standard (what auth-conformance enforces, and why)
	@set -eu; \
	work=$$(mktemp -d); \
	trap 'rm -rf "$$work"' EXIT; \
	if [ -n "$(AUTHCONF_FILE)" ] && [ -f "$(AUTHCONF_FILE)" ]; then \
	  cp "$(AUTHCONF_FILE)" "$$work/main.go"; \
	else \
	  curl -fsSL -o "$$work/main.go" "$(AUTHCONF_URL)"; \
	fi; \
	printf 'module authconformance\n\ngo 1.24\n' > "$$work/go.mod"; \
	( cd "$$work" && go run . --explain )

auth-conformance: ## Enforce the estate auth standard (code + chart)
	@set -eu; \
	work=$$(mktemp -d); \
	trap 'rm -rf "$$work"' EXIT; \
	if [ -n "$(AUTHCONF_FILE)" ] && [ -f "$(AUTHCONF_FILE)" ]; then \
	  echo "==> using local checker $(AUTHCONF_FILE)"; \
	  cp "$(AUTHCONF_FILE)" "$$work/main.go"; \
	else \
	  echo "==> fetching auth-conformance checker from $(AUTHCONF_URL)"; \
	  curl -fsSL -o "$$work/main.go" "$(AUTHCONF_URL)"; \
	fi; \
	printf 'module authconformance\n\ngo 1.24\n' > "$$work/go.mod"; \
	( cd "$$work" && go build -o "$$work/authconformance" . ); \
	"$$work/authconformance" "$$(pwd)"

# ── lint: mirror of the golangci-lint invocation in tasks/go-lint/pullrequest.yaml ──
#
# --timeout 15m matches the task; see task comment (Azure builder nodes hit
# 10m on cold-cache package-load). Locally the timeout is generous, not
# tight — matching the CI value keeps the two runs comparable.
lint: lint-config file-size swag-check auth-conformance ## Run golangci-lint against the merged config (+ swagger freshness + auth standard)
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

# ── swag: the OpenAPI spec must match the annotations ────────────────────
#
# WHY THIS LIVES HERE AND NOT IN EACH REPO.
#
# swag-check existed only in individual service Makefiles, and CI never ran any
# of them — the Tekton tasks invoke THIS file, which had no swagger concept. So
# the gate that stops a stale spec shipping ran nowhere: it fired only when a
# developer happened to type `make` locally. leartech-ai-gateway#30 would have
# merged a spec missing two response fields, and release.yaml publishes
# angular/typescript/go/python/rust clients from that spec, so five SDKs would
# have shipped without them. It was caught by hand, not by the pipeline.
#
# THE VERSION COMES FROM go.mod, DELIBERATELY.
#
# Every repo previously carried a separate SWAG_VERSION constant with a comment
# saying "kept in sync with go.mod". Measured 2026-09-08: three of four had
# drifted (v1.16.3, v1.16.4, v1.16.4 against go.mod's v1.16.6 everywhere). A
# second source of truth for a version is a second thing to forget. Reading
# go.mod removes the constant rather than policing it — the pattern is borrowed
# from mqube-ledger, which has done it this way for years.
#
# AND THE GUARD CHECKS THE VERSION, NOT JUST PRESENCE.
#
# The old guard was `if ! command -v swag`. Whichever swag you happened to have
# installed then won, and a different minor version emits a different spec — so
# `swag-check` reported "not in sync" against a perfectly good committed spec.
# That cost real debugging time on 2026-09-08 and looked exactly like a genuine
# drift. Install when absent OR when the version differs.
#
# SKIPS CLEANLY for repos with no swagger. Not every Go service has annotations,
# and this target is now on the `lint` path for all of them.

# swag's own --version output has been unreliable across patch releases, so the
# marker file records what we installed rather than asking the binary.
SWAG_MARKER ?= $(shell go env GOPATH)/bin/.swag-version

swag-version: ## Print the swag version this repo requires (from go.mod)
	@grep -E '^\s+github.com/swaggo/swag ' go.mod 2>/dev/null | awk '{print $$2}' \
	  || echo "(no swaggo/swag in go.mod)"

# Resolves the required version, installs only if missing or mismatched, and
# echoes the binary path. Used by both swag and swag-check.
define swag_ensure
	want=$$(grep -E '^[[:space:]]+github.com/swaggo/swag ' go.mod 2>/dev/null | awk '{print $$2}'); \
	if [ -z "$$want" ]; then echo "==> swag: no swaggo/swag in go.mod; nothing to do"; exit 0; fi; \
	have=$$(cat "$(SWAG_MARKER)" 2>/dev/null || echo none); \
	bin=$$(go env GOPATH)/bin/swag; \
	if [ ! -x "$$bin" ] || [ "$$have" != "$$want" ]; then \
	  echo "==> installing swag $$want (had $$have) — version taken from go.mod"; \
	  go install github.com/swaggo/swag/cmd/swag@$$want; \
	  printf '%s' "$$want" > "$(SWAG_MARKER)"; \
	fi
endef

# A repo has a spec to check only if docs/ is committed AND some file carries
# swaggo annotations. Both, because docs/ can linger after annotations are
# removed and vice versa.
define swag_applicable
	if [ ! -d docs ]; then echo "==> swag-check: no docs/ directory; skipping"; exit 0; fi; \
	if ! grep -rqE '^//[[:space:]]*@(title|Summary|Router)' --include='*.go' . 2>/dev/null; then \
	  echo "==> swag-check: no swaggo annotations found; skipping"; exit 0; \
	fi
endef

swag: ## Regenerate docs/ from the swaggo annotations (version from go.mod)
	@set -eu; \
	$(swag_applicable); \
	$(swag_ensure); \
	echo "==> swag init -g $(SWAG_ENTRYPOINT) -o docs"; \
	$$(go env GOPATH)/bin/swag init $(SWAG_FLAGS) -g $(SWAG_ENTRYPOINT) -o docs

# Per-repo overrides, read from OPTIONAL dotfiles so CI needs no per-repo
# invocation — the Tekton task runs this mk with no arguments, so anything
# repo-specific has to be discoverable from the working tree.
#
# .swagflags exists because leartech-auth-service needs
# `--parseDependency --useStructName`: without them swag emits full package
# paths as schema names (GithubComMikelearLeartechAuthServiceModelsTenant
# instead of Tenant), and those names become the generated SDK type names in
# five languages. Running the central check WITHOUT a repo's real flags would
# produce a different spec and report a false "stale" — the exact failure mode
# this whole change exists to remove, so it must not be reintroduced here.
SWAG_ENTRYPOINT ?= $(shell cat .swagentrypoint 2>/dev/null || echo cmd/server/main.go)
SWAG_FLAGS      ?= $(shell cat .swagflags 2>/dev/null)

swag-check: ## Fail if docs/ is stale against the annotations (renders to a temp dir)
	@set -eu; \
	$(swag_applicable); \
	$(swag_ensure); \
	tmp=$$(mktemp -d); \
	trap 'rm -rf "$$tmp"' EXIT; \
	$$(go env GOPATH)/bin/swag init $(SWAG_FLAGS) -g $(SWAG_ENTRYPOINT) -o "$$tmp" >/dev/null 2>&1 || { \
	  echo "FAIL: swag init failed — the annotations do not parse" >&2; exit 1; }; \
	rc=0; \
	for f in swagger.json swagger.yaml; do \
	  [ -f "docs/$$f" ] || { echo "FAIL: docs/$$f is missing" >&2; rc=1; continue; }; \
	  diff -q "docs/$$f" "$$tmp/$$f" >/dev/null 2>&1 || { \
	    echo "FAIL: docs/$$f is STALE against the annotations — run: make swag" >&2; rc=1; }; \
	done; \
	if [ $$rc -ne 0 ]; then \
	  echo "" >&2; \
	  echo "release.yaml publishes angular/typescript/go/python/rust clients from" >&2; \
	  echo "docs/swagger.json, so a stale spec ships SDKs missing endpoints." >&2; \
	  exit 1; \
	fi; \
	echo "==> swag-check: docs/ matches the annotations"

# ── file-size: the accumulation guard golangci-lint cannot provide ───────
#
# Reports hand-written .go files over $(FILE_SIZE_WARN) lines. Warn-only
# unless FILE_SIZE_FAIL > 0 (see the knobs block above for the rationale).
#
# EXCLUSIONS, and why each one matters:
#   *_test.go        table-driven suites are legitimately long; the estate's
#                    test:prod ratio runs ~1.45:1 and that's a good thing.
#   zz_generated*    controller-gen deepcopy output.
#   generated marker files whose first 20 lines carry `Code generated ...
#                    DO NOT EDIT` or `GENERATED BY SWAG`. The swaggo case is
#                    NOT optional: docs/docs.go is the single largest .go file
#                    in the estate (1,860 lines in leartech-auth-service) and
#                    it does not match the zz_generated glob. Without this,
#                    every swaggo service reports a false positive on day one.
#                    (Same reason the base config sets `generated: lax`.)
#   vendor/, .git/   not ours.
#
# THRESHOLDS are grounded in a survey of the Go estate, not picked at random.
# Largest hand-written files at time of writing:
#   3111  orchestrator-controller internal/controller/plan_controller.go
#   1009  mcp-servers internal/tektonserver/tools.go
#    962  mcp-servers internal/designserver/tools.go
#    883  orchestrator-controller api/v1alpha1/plan_types.go
#    879  auth-service internal/handlers/admin.go
# plan_controller.go is a ~3x outlier; everything else sits under 1200. So
# WARN=800 surfaces the handful worth a look, and FAIL=1200 (when someone
# chooses to enable it) would block exactly one file today — one decision,
# not an estate-wide fire drill.
#
# Portability: POSIX sh only. No `xargs -r` (absent on BSD/macOS xargs), no
# GNU-only find predicates — a developer on macOS gets the same result as the
# Tekton alpine/debian step.
file-size: ## Report hand-written .go files over $(FILE_SIZE_WARN) lines (warn-only unless FILE_SIZE_FAIL>0)
	@set -eu; \
	candidates=$$(mktemp); over=$$(mktemp); \
	trap 'rm -f "$$candidates" "$$over"' EXIT; \
	find . -type f -name '*.go' \
	  -not -name '*_test.go' \
	  -not -name 'zz_generated*' \
	  -not -path './vendor/*' \
	  -not -path './.git/*' > "$$candidates"; \
	if [ ! -s "$$candidates" ]; then \
	  echo "==> file-size: no hand-written Go files found; nothing to check"; \
	  exit 0; \
	fi; \
	while IFS= read -r f; do \
	  if head -20 "$$f" | grep -qE 'Code generated .* DO NOT EDIT|GENERATED BY SWAG'; then \
	    continue; \
	  fi; \
	  n=$$(awk 'END { print NR }' "$$f"); \
	  if [ "$$n" -gt "$(FILE_SIZE_WARN)" ]; then \
	    printf '%6d  %s\n' "$$n" "$$f"; \
	  fi; \
	done < "$$candidates" | sort -rn > "$$over"; \
	count=$$(awk 'END { print NR }' "$$over"); \
	if [ "$$count" -eq 0 ]; then \
	  echo "==> file-size: OK — no hand-written .go file over $(FILE_SIZE_WARN) lines"; \
	  exit 0; \
	fi; \
	echo "==> file-size: $$count file(s) over the $(FILE_SIZE_WARN)-line warn threshold"; \
	echo ""; \
	sed 's/^/    /' "$$over"; \
	echo ""; \
	echo "    No linter catches this: golangci-lint has no file-length check, and"; \
	echo "    every complexity linter it ships is per-FUNCTION. ai-review sees only"; \
	echo "    the diff, never the file it lands in. So file size accumulates with"; \
	echo "    no single PR ever being wrong — which is why it is reported here."; \
	echo ""; \
	echo "    IF YOU ARE AN AGENT DOING PRE-PUSH VALIDATION: this is ADVISORY."; \
	echo "    The exit code is 0 and this does NOT block your push. Treat lint as"; \
	echo "    GREEN. Do NOT split or restructure these files unless that IS your"; \
	echo "    assigned task — an unrequested refactor is scope creep and will be"; \
	echo "    rejected. If a file above is one you created or grew substantially"; \
	echo "    in THIS change, mention it in the PR description and move on."; \
	echo ""; \
	worst=$$(awk 'NR == 1 { print $$1 }' "$$over"); \
	if [ "$(FILE_SIZE_FAIL)" -gt 0 ] && [ "$$worst" -gt "$(FILE_SIZE_FAIL)" ]; then \
	  echo "==> file-size: FAIL — largest file is $$worst lines, over FILE_SIZE_FAIL=$(FILE_SIZE_FAIL)"; \
	  echo "    Split the file, or raise FILE_SIZE_FAIL in this repo's Makefile with a"; \
	  echo "    comment saying why."; \
	  exit 1; \
	fi; \
	echo "==> file-size: WARN ONLY (FILE_SIZE_FAIL=$(FILE_SIZE_FAIL)) — not blocking"

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
# test-coverage uses bash (process substitution + set -o pipefail); the go-test
# image (golang:1.26) has bash. Target-specific so `lint` stays /bin/sh-safe
# (golangci-lint image is alpine, no bash). Fixes "Syntax error: ( unexpected".
test-coverage: SHELL := /bin/bash
test-coverage: .SHELLFLAGS := -ec
test-coverage: ## Race + coverage, enforce floor + delta-vs-base
	@set -eo pipefail; \
	SCOPE="$(COVERAGE_SCOPE)"; \
	THRESHOLD="$(COVERAGE_THRESHOLD)"; \
	DELTA_TOL="$(COVERAGE_DELTA_TOLERANCE)"; \
	OVERRIDDEN=""; \
	[ "$$SCOPE"     != "$(COVERAGE_SCOPE_DEFAULT)" ]     && OVERRIDDEN="$$OVERRIDDEN COVERAGE_SCOPE=$$SCOPE(default:$(COVERAGE_SCOPE_DEFAULT))"; \
	[ "$$THRESHOLD" != "$(COVERAGE_THRESHOLD_DEFAULT)" ] && OVERRIDDEN="$$OVERRIDDEN COVERAGE_THRESHOLD=$$THRESHOLD(default:$(COVERAGE_THRESHOLD_DEFAULT))"; \
	[ "$$DELTA_TOL" != "$(COVERAGE_DELTA_TOLERANCE_DEFAULT)" ] && OVERRIDDEN="$$OVERRIDDEN COVERAGE_DELTA_TOLERANCE=$$DELTA_TOL(default:$(COVERAGE_DELTA_TOLERANCE_DEFAULT))"; \
	if [ -n "$$OVERRIDDEN" ]; then \
	  echo ""; \
	  echo "################################################################"; \
	  echo "## ⚠  COVERAGE GATE OVERRIDDEN — THIS RUN DOES NOT MATCH CI    ##"; \
	  echo "################################################################"; \
	  echo "##  overridden:$$OVERRIDDEN"; \
	  echo "##"; \
	  echo "##  The pinned defaults exist so a local run is BY CONSTRUCTION"; \
	  echo "##  identical to CI. Overriding them makes a green local run"; \
	  echo "##  meaningless as a pre-push signal — CI still enforces the"; \
	  echo "##  defaults and will fail on exactly what you relaxed."; \
	  echo "##"; \
	  echo "##  IF YOU ARE AN AGENT: do NOT override these to get green."; \
	  echo "##  Re-run with no COVERAGE_* set. If a default is genuinely"; \
	  echo "##  wrong that is a pipeline-catalog change, not a local flag."; \
	  echo "##  Record any override in the PR description — a reviewer"; \
	  echo "##  cannot otherwise tell your pre-push check was weakened."; \
	  echo "################################################################"; \
	  echo ""; \
	fi; \
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
pre-push: vet tidy-check build test-coverage lint vuln ## Full pre-push gate (vet tidy-check build test-coverage lint[+swag-check] vuln)
	@echo "==> pre-push gate: all checks passed"
