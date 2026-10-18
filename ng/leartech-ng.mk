# leartech-ng.mk — canonical Angular build/test/lint/audit targets for leartech
# front-end services, the Node/Angular sibling of go/leartech-go.mk.
#
# SINGLE source of truth for what the Tekton ng-lint / ng-test / angular /
# npm-audit tasks run and what the dev agent (and a developer) runs locally. If
# the two drift, this file is wrong. The Tekton tasks invoke the same commands
# the recipes below do:
#   - tasks/ng-lint/pullrequest.yaml   : npm ci --legacy-peer-deps + npm run lint
#   - tasks/ng-test/pullrequest.yaml   : npm ci + npm test + lcov coverage gate
#   - tasks/angular/pullrequest.yaml   : npm install + ng build
#   - tasks/npm-audit/pullrequest.yaml : npm audit --audit-level=$AUDIT_LEVEL
#
# THE TARGET NAMES MATCH go/leartech-go.mk ON PURPOSE. A language-neutral dev
# agent runs `make -f <baked mk> ci` (or lint/test/build/vuln) and the IMAGE
# supplies the language-correct implementation — so the agent carries no
# per-language logic. Keep lint / test / build / vuln / pre-push / ci / help
# named identically across every leartech-<lang>.mk.
#
# Two usage shapes, both supported (mirrors leartech-go.mk):
#   1) One-shot from a consumer repo (no include):
#        curl -fsSL https://raw.githubusercontent.com/mikelear/leartech-pipeline-catalog/main/ng/leartech-ng.mk -o leartech-ng.mk
#        make -f leartech-ng.mk ci
#   2) include'd from a repo-local Makefile:
#        include leartech-ng.mk
#
# Consumer-tunable variables (all ?= so a repo overrides before include):
#   NPM                 npm binary (default npm).
#   NG                  Angular CLI binary (default ng; the agent-ng image bakes it).
#   NPM_INSTALL_FLAGS   install flags (default --legacy-peer-deps, matching CI).
#   AUDIT_LEVEL         low|moderate|high|critical (default critical, matches CI).
#   COVERAGE_THRESHOLD  minimum line-coverage % (default 60.0, matches ng-test).

NPM                ?= npm
NG                 ?= ng
NPM_INSTALL_FLAGS  ?= --legacy-peer-deps
AUDIT_LEVEL        ?= critical
COVERAGE_THRESHOLD ?= 60.0

.PHONY: help preflight install lint test build vuln audit ci pre-push

## help: list the targets
help:
	@grep -E '^## ' $(firstword $(MAKEFILE_LIST)) | sed 's/^## //'

## preflight: fail early if the toolchain the recipes need is absent
preflight:
	@command -v $(NPM) >/dev/null 2>&1 || { echo "FAIL: npm not on PATH (the agent-ng image bakes it)"; exit 1; }
	@command -v $(NG)  >/dev/null 2>&1 || { echo "FAIL: ng not on PATH (the agent-ng image bakes @angular/cli)"; exit 1; }
	@echo "preflight: npm $$($(NPM) --version), ng present"

## install: npm ci (falls back to npm install), exactly as every ng Tekton task does
install:
	$(NPM) ci $(NPM_INSTALL_FLAGS) || $(NPM) install $(NPM_INSTALL_FLAGS)

## lint: npm run lint — the ng-lint task
lint: install
	$(NPM) run lint

## test: ng test headless with coverage, then enforce COVERAGE_THRESHOLD from lcov
##       — the ng-test task's gate, reproduced offline.
test: install
	$(NPM) test
	@LCOV=$$(find coverage -name 'lcov.info' -type f 2>/dev/null | head -1); \
	if [ -z "$$LCOV" ]; then echo "FAIL: no lcov.info produced by Karma — did the suite run?"; exit 1; fi; \
	LF=$$(awk -F: '/^LF:/ { s += $$2 } END { print s+0 }' "$$LCOV"); \
	LH=$$(awk -F: '/^LH:/ { s += $$2 } END { print s+0 }' "$$LCOV"); \
	TOTAL=$$(awk -v h="$$LH" -v f="$$LF" 'BEGIN { if (f==0) print "0.0"; else printf "%.1f",(h/f)*100 }'); \
	echo "total=$${TOTAL}%  threshold=$(COVERAGE_THRESHOLD)%"; \
	if awk -v t="$$TOTAL" -v th="$(COVERAGE_THRESHOLD)" 'BEGIN { exit !(t < th) }'; then \
	  echo "FAIL: coverage $${TOTAL}% below threshold $(COVERAGE_THRESHOLD)%"; exit 1; \
	fi

## build: ng build (production) — the angular task's build step
build: install
	$(NG) build

## vuln: npm audit at AUDIT_LEVEL — the npm-audit task. Aliased from the
##       shared contract name `vuln` (go's govulncheck target) so the neutral
##       agent calls `make vuln` for any language; `audit` is the ng-native alias.
vuln audit:
	@[ -f package-lock.json ] || $(NPM) install $(NPM_INSTALL_FLAGS) --package-lock-only
	$(NPM) audit --audit-level=$(AUDIT_LEVEL)

## ci: the full CI-equivalent gate — what the dev agent drives to green before it pushes.
ci: lint test build vuln
	@echo "ci: lint + test + build + vuln all passed"

## pre-push: alias for ci (parity with go/leartech-go.mk's pre-push)
pre-push: ci
