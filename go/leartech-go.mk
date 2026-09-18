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

# dockerfile-lint: hadolint over every Dockerfile in the repo.
#
# HADOLINT_VERSION is pinned here rather than floating, because a hadolint
# bump adds RULES -- v2.14.0 -> v2.15.1 introduced DL3066 and turned four
# leartech-dockerfiles builds red at once with no source change. Pinning in
# one place means that arrives as one reviewed PR instead of a surprise.
# renovate: datasource=docker depName=hadolint/hadolint
HADOLINT_VERSION ?= v2.15.1-alpine
HADOLINT_IMAGE ?= hadolint/hadolint:$(HADOLINT_VERSION)
#
# error, not hadolint's default of info. Measured across the 46 repos with a
# Dockerfile on 2026-09-14:
#
#   --failure-threshold error       1 of 46 fail
#   --failure-threshold warning    32 of 46
#   --failure-threshold info       37 of 46   (the default)
#
# A gate that reds 37 of 46 repos on the day it ships is not a gate, it is an
# outage everyone learns to route around. `error` can be switched on today;
# the warning backlog (DL3008 unpinned apt, DL3045 COPY without WORKDIR,
# DL3018 unpinned apk) gets burned down and the bar raised after.
#
# leartech-dockerfiles deliberately runs STRICTER than this in its own
# pipeline, at hadolint's default. That repo's product IS Dockerfiles, and
# that strictness is what caught DL3066.
HADOLINT_THRESHOLD ?= error

# job-reaping: a Job that never expires is a leak with a green tick. Same
# curl-or-local shape.
JOBREAP_URL ?= https://raw.githubusercontent.com/mikelear/leartech-pipeline-catalog/main/go/jobreaping/main.go
JOBREAP_FILE ?=

# comment-gate: challenges added prose. Same curl-or-local shape.
#
# COMMENTGATE_BASE is the ref the diff is taken against. Lighthouse sets
# PULL_BASE_REF on a PR build; locally it falls back to main. The gate examines
# CHANGED LINES ONLY, so existing comments in a repo are never a blocker —
# a ratchet demanding an estate-wide cleanup before anything could merge would
# stall every repo, and the gap closes as files are touched.
COMMENTGATE_URL ?= https://raw.githubusercontent.com/mikelear/leartech-pipeline-catalog/main/go/commentgate/main.go
COMMENTGATE_FILE ?=
COMMENTGATE_BASE ?= origin/$(if $(PULL_BASE_REF),$(PULL_BASE_REF),main)

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
# A GOCOVERDIR written by a coverage-instrumented binary (`go build -cover`)
# while the end2end suite drove it. When set, those counters are merged with
# the unit-test counters before the floor is computed.
#
# WHY. The floor measures statements executed by `go test`. An end2end suite
# drives a deployed pod over HTTP, so it contributes NOTHING, however thorough
# it is. leartech-maestro-service measured 56.4% against a 60% floor while its
# uncovered blocks were precisely the Mongo repositories and informer callbacks
# that end2end exercises every run. The test effort was real and the metric
# could not see it.
#
# HOW TO PRODUCE ONE. Build the service with
#
#     go build -cover -covermode=atomic -o <bin> ./cmd/<service>
#
# and set GOCOVERDIR to a writable directory on the pod. Three traps, all
# measured rather than assumed:
#
#   * -covermode=atomic is REQUIRED, not optional. This target runs unit tests
#     with -race, which forces atomic counters, and `go build -cover` defaults
#     to set. covdata then refuses the merge outright with "counter mode clash
#     while reading meta-data file ... previous file had atomic, new file has
#     set" — a hard error, so at least it is loud.
#
#   * Do NOT pass -coverpkg to `go build -cover`. The binary still reports
#     `-cover=true` under `go version -m`, and writes NOTHING at all. With
#     -coverpkg the same build produced an empty GOCOVERDIR; without it, the
#     expected covmeta + covcounters pair. Instrument the whole module and let
#     the -pkg filter below restrict the output to COVERAGE_SCOPE, so the total
#     stays comparable to the floor.
#
#   * The process must return from main. Counters are flushed by an exit hook,
#     which runs on a normal return and on os.Exit, but NOT on SIGKILL and not
#     on a SIGTERM the process does not handle. Either of those leaves a
#     covmeta-only directory that reads as 0%, so the check below refuses it.
#
# Deliberately NOT a default: a repo with no instrumented build must keep
# measuring exactly what it measures today.
E2E_COVERAGE_DIR ?=
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

.PHONY: auth-conformance auth-standard help lint-config lint file-size vet tidy-check test test-coverage test-integration build vuln pre-push preflight preflight-doctor

# ── how a checker is obtained ────────────────────────────────────────────────
#
# Three ways, tried in order, and the order is the point.
#
#   1. a local file          *_FILE, when set — lets this repo dogfood its own
#                            checker source without a round trip
#   2. a binary on PATH      shipped by ghcr.io/mikelear/leartech-checkers,
#                            which is the step image in CI and is COPY'd into
#                            the agent base image
#   3. curl + go run         the original path, kept as a fallback
#
# WHY THE BINARY IS PREFERRED. The curl path costs three things the estate has
# already paid for: raw.githubusercontent serves cache-control: max-age=300, so
# a merged change is not live for five minutes and a fix can look like it did
# not work; it needs a Go toolchain in the running container, which is why only
# leartech-agent-go can pre-flight these; and it recompiles on every run of
# every PR in every repo.
#
# WHY THE FALLBACK STAYS. Removing it would make every repo depend on the image
# having rolled out first. A gate that cannot run is worse than a slow one, and
# this estate has twice turned a delivery change into a fleet-wide stop.
#
# ── preflight: run what CI runs, before pushing ──────────────────────────────
#
# One command instead of folklore about which targets matter. Intended for a
# person or an agent about to push, and for `make preflight` inside an agent
# container on the Controller.
#
# WHAT IT CANNOT TELL YOU, stated here rather than discovered later. A green
# preflight is "most things are fine", never "CI will pass". Measured on
# 2026-09-14/15, every one of these was green locally and red in CI:
#
#   the linter    the repo's own .golangci.yml is STRICTER than CI's merged
#                 config, so it reported three errcheck hits CI excludes --
#                 work against a rule this estate does not run
#   the image     a step verified with docker on a laptop failed in CI, where
#                 the lint container has no docker binary at all
#   the shell     a script checked without `set -eo pipefail` cannot show that
#                 a failed command substitution kills the step
#   the arch      govulncheck panicked on amd64 CI and passed six times on
#                 local arm64
#
# So preflight deliberately runs the SAME fetched checkers CI does, rather
# than local equivalents, and `preflight-doctor` prints what it still cannot
# cover. Anything it does catch is a ten-minute CI cycle saved.
preflight: lint test vuln ## Run the gates CI runs, before pushing (see: preflight-doctor)
	@echo ""
	@echo "==> preflight: lint, test and vuln passed"
	@echo "    This is NOT a guarantee CI will pass. Run 'make preflight-doctor'"
	@echo "    for what it cannot see."

preflight-doctor: ## Print what preflight does and does NOT cover
	@echo "==> preflight covers"
	@echo "    lint          golangci-lint against the MERGED config (base + repo),"
	@echo "                  plus auth-conformance, job-reaping and the comment gate,"
	@echo "                  all fetched from the catalog so they are the same code CI runs"
	@echo "    test          go test ./... -race"
	@echo "    vuln          govulncheck ./..."
	@echo ""
	@echo "==> preflight does NOT cover, and each of these has cost a red CI run"
	@echo "    container     a step that needs docker, or a specific step image."
	@echo "                  dockerfile-lint runs as its own Tekton step for exactly"
	@echo "                  this reason; the go-lint container has no docker binary."
	@echo "    architecture  CI builds amd64. govulncheck panicked there and passed"
	@echo "                  six times on local arm64."
	@echo "    the preview   end2end, end2end-ui and the dynamic scans need a deployed"
	@echo "                  preview. Nothing local can stand in for one."
	@echo "    the cluster   image-scan, qa-gate and anything reading a live namespace."
	@echo "    freshness     the catalog's checkers are fetched from raw.githubusercontent,"
	@echo "                  which serves cache-control: max-age=300. For five minutes"
	@echo "                  after a catalog merge, local and CI can legitimately differ."
	@echo ""
	@echo "==> the honest summary"
	@echo "    A green preflight means most classes of failure are ruled out."
	@echo "    It does not mean the PR will build."

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
	@echo "    E2E_COVERAGE_DIR          $(if $(E2E_COVERAGE_DIR),$(E2E_COVERAGE_DIR),(unset — unit coverage only))"
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

# ── dockerfile-lint: every repo builds a container before it builds a chart ──
#
# 45 of the 46 repos with a Dockerfile did not lint it. The one that did was
# leartech-dockerfiles, whose product is Dockerfiles -- so the only repo
# checking its images was the one that makes images for everyone else.
#
# Skips loudly rather than silently when there is no Dockerfile: a repo with
# nothing to check and a repo whose scan failed to find anything must not
# produce the same output.
# NOT a prerequisite of `lint`, and that was a mistake worth recording.
#
# `lint` runs inside golangci/golangci-lint, which has no docker binary --
# and a Tekton step has no docker daemon to talk to even if it did. Wiring
# this into `lint` therefore failed EVERY repo with
# "docker not on PATH; cannot run hadolint", including repos with no
# Dockerfile at all, because the guard refusing to pass a check that did not
# run fired before the no-Dockerfile skip.
#
# The guard was right; the placement was wrong. hadolint needs its own
# pipeline step running the hadolint IMAGE, the way leartech-dockerfiles
# already does it -- not a docker call from inside another tool's container.
#
# Kept as a target because it is genuinely useful locally, where docker does
# exist, and `make dockerfile-lint` before pushing is worth having.
dockerfile-lint: ## hadolint every Dockerfile locally (threshold: $(HADOLINT_THRESHOLD))
	@set -eu; \
	files=$$(find . -name 'Dockerfile*' \
	           -not -path './.git/*' \
	           -not -path '*/node_modules/*' \
	           -not -path '*/vendor/*' \
	           -not -path './versionStream/*' 2>/dev/null | sort); \
	if [ -z "$$files" ]; then \
	  echo "==> dockerfile-lint: no Dockerfile in this repo; skipping"; \
	  exit 0; \
	fi; \
	if ! command -v docker >/dev/null 2>&1; then \
	  echo "==> dockerfile-lint: docker not on PATH; cannot run $(HADOLINT_IMAGE)" >&2; \
	  echo "    Refusing to report success for a check that did not run." >&2; \
	  exit 1; \
	fi; \
	echo "==> dockerfile-lint: $(HADOLINT_IMAGE), --failure-threshold $(HADOLINT_THRESHOLD)"; \
	rc=0; n=0; \
	for f in $$files; do \
	  n=$$((n + 1)); \
	  out=$$(docker run --rm -i $(HADOLINT_IMAGE) \
	           hadolint --failure-threshold $(HADOLINT_THRESHOLD) - < "$$f" 2>&1) || rc=1; \
	  if [ -n "$$out" ]; then \
	    echo "  $$f"; \
	    echo "$$out" | sed 's/^/      /'; \
	  else \
	    echo "  $$f  clean"; \
	  fi; \
	done; \
	echo "==> dockerfile-lint: checked $$n Dockerfile(s)"; \
	if [ "$$rc" != "0" ]; then \
	  echo "" >&2; \
	  echo "Findings at or above $(HADOLINT_THRESHOLD) block this build. Lower-severity" >&2; \
	  echo "findings are printed above and do not: they are the backlog to clear" >&2; \
	  echo "before HADOLINT_THRESHOLD is raised." >&2; \
	fi; \
	exit "$$rc"

# ── job-reaping: nothing cleans up after a Job unless you say so ─────────────
#
# Measured 2026-09-14: the two build clusters held 12,046 Jobs between them and
# 10,674 were a single un-reaped source, the oldest 244 days old. Purging took
# gcp from 7,000 Jobs to 750 and az from 5,053 to 622. Nothing had reported a
# problem, because nothing was broken in a way anything watches.
#
# Checks three surfaces. The third is the one that matters: a Job created by a
# SERVICE at runtime has no owner, no helm release and no chart, so a
# template-only audit cannot see it -- which is exactly how arrivals-observer
# came to leave 205 completed forensics pods on one cluster and 11 on the other.
#
# CronJobs pass on history limits OR a TTL, because history limits genuinely
# bound them. 15 of the estate's 26 Job templates were already compliant that
# way; a check that failed them would be wrong, not strict.
job-reaping: ## Fail Jobs that nothing will ever reap (charts + Go)
	@set -eu; \
	work=$$(mktemp -d); \
	trap 'rm -rf "$$work"' EXIT; \
	if [ -n "$(JOBREAP_FILE)" ] && [ -f "$(JOBREAP_FILE)" ]; then \
	  echo "==> using local checker $(JOBREAP_FILE)"; \
	  cp "$(JOBREAP_FILE)" "$$work/main.go"; \
	elif command -v jobreaping >/dev/null 2>&1; then \
	  echo "==> using the jobreaping binary on PATH (leartech-checkers image)"; \
	  jobreaping -root "$$(pwd)"; \
	  exit $$?; \
	else \
	  echo "==> fetching job-reaping checker from $(JOBREAP_URL)"; \
	  curl -fsSL -o "$$work/main.go" "$(JOBREAP_URL)"; \
	fi; \
	printf 'module jobreaping\n\ngo 1.24\n' > "$$work/go.mod"; \
	( cd "$$work" && go build -o "$$work/jobreaping" . ); \
	"$$work/jobreaping" -root "$$(pwd)"

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
# ── comment-gate: prose must earn its place ──────────────────────────────────
#
# Two rules, both on CHANGED LINES only:
#
#   1. a change may not add more prose comment lines than test lines
#   2. an added comment asserting behaviour must name the test proving it,
#      as `proven-by: TestName`, and that test must exist in THIS repo
#
# Functional directives are exempt: they instruct a tool rather than asserting
# anything, so they cannot be false the way prose can.
comment-gate: ## Challenge added prose: ratchet, and claims must name a proof
	@set -eu; \
	work=$$(mktemp -d); \
	trap 'rm -rf "$$work"' EXIT; \
	if [ -n "$(COMMENTGATE_FILE)" ] && [ -f "$(COMMENTGATE_FILE)" ]; then \
	  echo "==> using local comment gate $(COMMENTGATE_FILE)"; \
	  cp "$(COMMENTGATE_FILE)" "$$work/main.go"; \
	else \
	  echo "==> fetching comment gate from $(COMMENTGATE_URL)"; \
	  curl -fsSL -o "$$work/main.go" "$(COMMENTGATE_URL)"; \
	fi; \
	printf 'module commentgate\n\ngo 1.24\n' > "$$work/go.mod"; \
	( cd "$$work" && go build -o "$$work/commentgate" . ); \
	git fetch -q origin "$(if $(PULL_BASE_REF),$(PULL_BASE_REF),main)" 2>/dev/null || true; \
	"$$work/commentgate" -base "$(COMMENTGATE_BASE)"


lint: lint-config file-size swag-check auth-conformance job-reaping comment-gate ## Run golangci-lint against the merged config (+ swagger freshness + auth standard + prose gate)
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
# ── test-integration: tests that need a real datastore ───────────────────
#
# WHY THIS IS HERE AND NOT IN A SERVICE MAKEFILE. leartech-auth-service has had
# a testcontainers Postgres harness for months (internal/store/postgres_test.go,
# //go:build integration) and CI has never run it: the go-test task invokes
# `test-coverage` from THIS file, which passes no `-tags`, so a tagged file is
# not compiled. Its `make integration-local` fires only when a developer types
# it. leartech-ai-gateway has just grown the same harness with the same problem.
#
# That is the swag-check failure exactly, recorded above: a gate that existed
# only in service Makefiles, which CI never invoked, so the thing it protected
# was unprotected. The fix is the same — put it where the Tekton tasks already
# look.
#
# OPT-IN BY DETECTION, NOT BY FLAG. The target discovers whether the repo has
# any INTEGRATION_TAG-tagged test files. A repo with none passes with a stated
# reason; a repo that adds one is covered without editing a variable someone
# has to remember. A flag would reproduce the failure this target exists to fix.
#
# AND IT DOES NOT SKIP WHEN IT CANNOT RUN. If a repo declares integration tests
# and no datastore is reachable, this FAILS. The estate's own rule, from
# end2end/run.sh: "a green tick from an unconfigured run says the opposite of
# what it means."
#
# TWO WAYS TO GET A DATASTORE, and the pipeline needs the second one:
#
#   TEST_DATABASE_URL set  -> the tests use it and start nothing. This is the
#                             CI path: a Tekton SIDECAR runs Postgres and the
#                             task exports the DSN. No Docker socket required.
#   unset                  -> the tests start a container themselves
#                             (testcontainers). This is the local path.
#
# THE PIPELINE CANNOT USE THE CONTAINER PATH TODAY, and it is worth being exact
# about why rather than assuming envtest is a precedent: the go-test task runs
# envtest, but envtest is a real apiserver and etcd as PROCESSES inside
# golang:1.27 — no Docker socket, no DinD. So container-backed tests have no
# existing path in these pipelines, and wiring one would mean either a
# privileged DinD step (against the posture the kyverno registry policy
# implies) or a sidecar. A sidecar is the cheap answer and needs no new trust.
#
# COVERAGE IS DELIBERATELY NOT COLLECTED HERE. test-coverage enforces a floor
# and a +/-0.5 delta against base; integration tests would move store-layer
# coverage by far more than that and turn a real improvement into a gate
# failure. auth-service already hit this and solved it the same way — its
# validate_test.go says so: a unit companion "so the guard contributes to the
# coverage gate without requiring testcontainers". Two separate signals.
INTEGRATION_TAG ?= integration
INTEGRATION_SCOPE ?= ./...

test-integration: SHELL := /bin/bash
test-integration: .SHELLFLAGS := -ec
test-integration: ## Run INTEGRATION_TAG-tagged tests against a real datastore
	@tagged=$$(grep -rl --include='*_test.go' -E '^//go:build ($(INTEGRATION_TAG)$$|.*[[:space:]]$(INTEGRATION_TAG)([[:space:]]|$$))' . 2>/dev/null | head -20); \
	if [ -z "$$tagged" ]; then \
	  echo "==> test-integration: no *_test.go carries //go:build $(INTEGRATION_TAG) — nothing to run"; \
	  echo "    (this is a pass: the repo declares no datastore-backed tests)"; \
	  exit 0; \
	fi; \
	echo "==> test-integration: $(INTEGRATION_TAG)-tagged files:"; echo "$$tagged" | sed 's/^/      /'; \
	if [ -n "$${TEST_DATABASE_URL:-}" ]; then \
	  echo "==> using TEST_DATABASE_URL (no container started)"; \
	elif docker info >/dev/null 2>&1; then \
	  sock=$$(docker context inspect 2>/dev/null | sed -n 's/.*"Host": "\(unix:[^"]*\)".*/\1/p' | head -1); \
	  if [ -n "$$sock" ]; then \
	    echo "==> starting containers via $$sock"; \
	    export DOCKER_HOST="$$sock"; \
	    export TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=$${TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE:-/var/run/docker.sock}; \
	  fi; \
	else \
	  echo "FAIL: this repo declares $(INTEGRATION_TAG)-tagged tests and no datastore is reachable." >&2; \
	  echo "  Set TEST_DATABASE_URL (the CI path — a Postgres sidecar), or start docker/colima" >&2; \
	  echo "  locally. NOT skipped: a green tick from an unconfigured run says the opposite" >&2; \
	  echo "  of what it means." >&2; \
	  exit 1; \
	fi; \
	$(GO) test -tags $(INTEGRATION_TAG) -count=1 $(INTEGRATION_SCOPE)

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
	echo "=== go test -race -cover (scope=$$SCOPE, threshold=$$THRESHOLD%) ==="; \
	UNIT_COVERDIR=$$(mktemp -d); \
	trap 'rm -rf "$$UNIT_COVERDIR"' EXIT; \
	go test ./... -v -count=1 -race -coverpkg="$$SCOPE" -cover -args -test.gocoverdir="$$UNIT_COVERDIR"; \
	MODULE=$$(go list -m); \
	PKG_PATTERN=$$(printf '%s' "$$SCOPE" | sed "s#^\./#$$MODULE/#"); \
	if [ -n "$(E2E_COVERAGE_DIR)" ]; then \
	  echo; \
	  echo "=== merging end2end coverage from $(E2E_COVERAGE_DIR) ==="; \
	  if [ ! -d "$(E2E_COVERAGE_DIR)" ]; then \
	    echo "FAIL: E2E_COVERAGE_DIR=$(E2E_COVERAGE_DIR) does not exist." >&2; \
	    echo "      Unset it, or fix the collection step. Carrying on with unit" >&2; \
	    echo "      coverage alone would silently lower the number it is meant to raise." >&2; \
	    exit 1; \
	  fi; \
	  if ! ls "$(E2E_COVERAGE_DIR)"/covcounters.* >/dev/null 2>&1; then \
	    echo "FAIL: $(E2E_COVERAGE_DIR) holds no covcounters.* file." >&2; \
	    echo "      A covmeta-only directory is what you get when the process was" >&2; \
	    echo "      SIGKILLed rather than shut down gracefully, and go tool covdata" >&2; \
	    echo "      reports it as 0%% — indistinguishable from code that never ran." >&2; \
	    echo "      Check the pod handles SIGTERM and that terminationGracePeriodSeconds" >&2; \
	    echo "      is long enough for it to return from main." >&2; \
	    ls -la "$(E2E_COVERAGE_DIR)" >&2 || true; \
	    exit 1; \
	  fi; \
	  MERGED=$$(mktemp -d); \
	  go tool covdata merge -i="$$UNIT_COVERDIR,$(E2E_COVERAGE_DIR)" -o="$$MERGED"; \
	  go tool covdata textfmt -i="$$MERGED" -pkg="$$PKG_PATTERN" -o=cover.out; \
	  rm -rf "$$MERGED"; \
	  echo "==> merged unit + end2end counters"; \
	else \
	  go tool covdata textfmt -i="$$UNIT_COVERDIR" -pkg="$$PKG_PATTERN" -o=cover.out; \
	fi; \
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
