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
COPY go/authgate/         ./authgate/

# CGO off and -trimpath so the binaries are static and reproducible: an agent
# and a CI step must be running the same bytes, not merely the same version.
ENV CGO_ENABLED=0
RUN set -eu; \
    for m in authconformance authcoverage authtoken commentgate jobreaping renovateenrolment versionanchor scopedrift e2eskipcontract authgate; do \
      cd "/src/$m"; \
      go mod download; \
      go build -trimpath -ldflags="-s -w" -o "/out/$m" .; \
    done

# A shell is required: these run as Tekton step images, and a Tekton `script:`
# block needs an interpreter. scratch would be smaller and unusable.
FROM alpine:3.24
RUN apk add --no-cache ca-certificates git
COPY --from=build /out/ /usr/local/bin/

# Fail the BUILD if a binary did not land or cannot run. An image that ships
# a missing checker would make every consumer skip it silently, which is the
# exact failure mode these checkers exist to catch.
RUN set -eu; \
    for b in authconformance authcoverage authtoken commentgate jobreaping renovateenrolment versionanchor scopedrift e2eskipcontract authgate; do \
      test -x "/usr/local/bin/$b" || { echo "FAIL: $b did not build into the image" >&2; exit 1; }; \
    done; \
    echo "==> leartech-checkers: 10 binaries present and executable"

LABEL org.opencontainers.image.source=https://github.com/mikelear/leartech-pipeline-catalog
LABEL org.opencontainers.image.description="The leartech pipeline catalog's gates as pinned binaries"
