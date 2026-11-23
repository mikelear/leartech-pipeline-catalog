# The catalog's gates, as one pinned artefact.
#
# WHY THIS IMAGE EXISTS. Every gate here — auth-conformance, the comment
# gate, job-reaping, authtoken, auth-coverage — was delivered by curling
# main.go from raw.githubusercontent and running `go run`. That works, and it
# has three costs the estate has now paid:
#
#   raw.githubusercontent serves cache-control: max-age=300, so a merged
#   change is NOT live for five minutes. On 2026-09-15 a fix to the lint
#   target looked like it had not worked, because the run fetched inside that
#   window.
#
#   it needs a Go toolchain in whatever container runs it, which is why only
#   leartech-agent-go can pre-flight these today. A Python, Rust or Angular
#   agent finds out in CI.
#
#   it recompiles on every run, in every repo, on every PR.
#
# Shipping the binaries instead means CI and every agent run the IDENTICAL
# artefact rather than merely the same source, the toolchain requirement
# disappears, and the version is a tag Renovate can see.
#
# Built from this repo so the source and the artefact cannot drift apart.

FROM golang:1.27-alpine AS build
RUN apk add --no-cache git
WORKDIR /src

# Each checker is its own module, so each is built on its own. Copying the
# module directories separately keeps a change in one from rebuilding all.
COPY go/authconformance/ ./authconformance/
COPY go/authcoverage/    ./authcoverage/
COPY go/authtoken/       ./authtoken/
COPY go/commentgate/     ./commentgate/
COPY go/jobreaping/      ./jobreaping/
COPY go/renovateenrolment/ ./renovateenrolment/
COPY go/versionanchor/    ./versionanchor/
COPY go/scopedrift/       ./scopedrift/
COPY go/e2eskipcontract/  ./e2eskipcontract/
COPY go/e2erun/         ./e2erun/
COPY go/pincheck/       ./pincheck/
COPY go/authgate/         ./authgate/
COPY go/keeperaccess/     ./keeperaccess/
COPY go/e2e/              ./e2e/
COPY go/prpost/           ./prpost/
COPY go/chartstd/          ./chartstd/
COPY go/argohooks/         ./argohooks/
COPY go/valuesstd/         ./valuesstd/
COPY go/identitystd/       ./identitystd/
COPY go/triggersync/       ./triggersync/
COPY go/selfprobe/         ./selfprobe/

# CGO off and -trimpath so the binaries are static and reproducible: an agent
# and a CI step must be running the same bytes, not merely the same version.
ENV CGO_ENABLED=0
RUN set -eu; \
    for m in authconformance authcoverage authtoken commentgate jobreaping renovateenrolment versionanchor scopedrift e2eskipcontract authgate e2erun pincheck keeperaccess e2e prpost chartstd argohooks valuesstd identitystd triggersync selfprobe; do \
      cd "/src/$m"; \
      go mod download; \
      go build -trimpath -ldflags="-s -w" -o "/out/$m" .; \
    done

# A shell is required: these run as Tekton step images, and a Tekton `script:`
# block needs an interpreter. scratch would be smaller and unusable.
FROM alpine:3.24
# The tools a consumer STEP needs, not just the ones the checkers need.
#
# WHY THEY BELONG HERE. A Tekton task's steps are separate containers that share only
# /workspace, so a tool installed in one step is not in the next. The end2end task used
# to apt-get curl and download kubectl in its FIRST step and then run four more in
# plain debian:12-slim — where neither exists. The wait phase therefore reported
# "kubectl or PULL_PULL_SHA unavailable" and probed a Service URL it had never resolved,
# and the failure read as a broken preview rather than a container with no curl.
#
# Every step that needs a shell tool can now run in THIS image and have it, so the
# class is gone rather than repeatedly patched. Installed once, at build time, instead
# of apt-get per run.
RUN apk add --no-cache ca-certificates git bash curl jq openssl python3
# PYTHON3, because a CONSUMER's end2end checks pipe into it and this is the step image they run in.
#
# Measured on leartech-ba-service#238/#239 (an empty commit on main failed identically, so it is
# pre-existing): the end2end pod reported
#
#   /workspace/source/end2end/07-s2s-audience.sh: line 26: python3: command not found
#   FAIL  aud does NOT contain leartech-ba-service ->   (allow-list rejected it silently)
#
# and, where the same script does `curl … | python3 -c '…'`, the missing interpreter surfaces as a
# WRITE error instead:
#
#   curl: Failed writing body
#   FAIL  register returned no sessionId
#
# Two different messages, one cause: every check that parses JSON with python3 failed, and the suite
# scored 4/10 with 14 skipped on a preview that was answering correctly. The estate's end2end packs use
# python3 throughout (they were written for the golang CI image, which has it), so the step image is
# where it belongs — the same argument as helm, which this file already carries for the same reason.
# HELM, because the chart-standard gate RENDERS and this image is where the render must happen.
#
# Measured on leartech-console-api#37: the task's `render` step ran in jx-boot (has helm, no chartstd)
# and called `chartstd -list-toggles`, which is in THIS image — so the behavioural half of the gate
# (`helm template` with each declared off-switch flipped) has never run on any repo that declares
# toggles, and a repo declaring NONE passed because of an `if [ -f "$decl" ]` guard. The gate rewarded
# not declaring them, and the ones that did went red with a message naming the toggles FILE.
#
# Pinned by version with a digest check at build time, so the image is reproducible from its tag alone —
# the same rule kubectl above follows.
ARG HELM_VERSION=v3.16.4
RUN set -eu; \
    curl -fsSL -o /tmp/helm.tgz "https://get.helm.sh/helm-${HELM_VERSION}-linux-amd64.tar.gz"; \
    tar -xzf /tmp/helm.tgz -C /tmp; \
    mv /tmp/linux-amd64/helm /usr/local/bin/helm; \
    chmod +x /usr/local/bin/helm; \
    helm version --short
# kubectl from the release channel, pinned by digest-of-version at build time so the
# image is reproducible from its tag alone.
RUN set -eu; \
    v=$(curl -fsSL https://dl.k8s.io/release/stable.txt); \
    curl -fsSL -o /usr/local/bin/kubectl "https://dl.k8s.io/release/${v}/bin/linux/amd64/kubectl"; \
    chmod +x /usr/local/bin/kubectl; \
    kubectl version --client
COPY --from=build /out/ /usr/local/bin/
# The shared go-test makefile ships here too, for the same reason the binaries do:
# a task that curls it from raw.githubusercontent gets whatever that CDN is serving,
# which is up to five minutes behind a merge. Pinned to this tag, a consumer's
# coverage behaviour is a function of the tag Renovate bumps.
COPY go/leartech-go.mk /usr/local/share/leartech-go.mk
COPY rust/deny.base.toml /usr/local/share/deny.base.toml

# Fail the BUILD if a binary did not land or cannot run. An image that ships
# a missing checker would make every consumer skip it silently, which is the
# exact failure mode these checkers exist to catch.
RUN set -eu; \
    for b in authconformance authcoverage authtoken commentgate jobreaping renovateenrolment versionanchor scopedrift e2eskipcontract authgate e2erun pincheck keeperaccess e2e prpost chartstd argohooks valuesstd identitystd triggersync selfprobe; do \
      test -x "/usr/local/bin/$b" || { echo "FAIL: $b did not build into the image" >&2; exit 1; }; \
    done; \
    test -f /usr/local/share/leartech-go.mk || { echo "FAIL: leartech-go.mk did not land in the image" >&2; exit 1; }; \
    test -f /usr/local/share/deny.base.toml || { echo "FAIL: deny.base.toml did not land in the image" >&2; exit 1; }; \
    for t in bash curl jq openssl python3 kubectl helm; do \
      command -v "$t" >/dev/null || { echo "FAIL: $t missing from the runtime image; a consumer step would have to install it" >&2; exit 1; }; \
    done; \
    echo "==> leartech-checkers: $(ls /usr/local/bin | wc -l) binaries, leartech-go.mk, and the step tools (bash curl jq openssl kubectl)"

LABEL org.opencontainers.image.source=https://github.com/mikelear/leartech-pipeline-catalog
LABEL org.opencontainers.image.description="The leartech pipeline catalog's gates as pinned binaries"
