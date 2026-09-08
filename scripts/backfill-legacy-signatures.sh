#!/usr/bin/env bash
# Backfill legacy .sig tags for images already signed as OCI 1.1 referrers.
#
# WHY THIS EXISTS
# ---------------
# cosign v3 changed its default signature storage from the tag convention
# (<repo>:sha256-<digest>.sig) to OCI 1.1 referrers. Sigstore's docs: "all new
# images signed with cosign v3 are shown as not signed by systems that don't
# support OCI 1.1 referrers." Kyverno's key attestor is one of those systems,
# so on 2026-09-08 verify-image-signatures reported 1,271 fail / 0 pass on GCP
# with ".attestors[0].entries[0].keys: no signatures found" — for images that
# ARE correctly signed (cosign verify succeeds on all of them).
#
# tasks/release/cosign-sign.yaml now passes --registry-referrers-mode=legacy,
# so NEW releases get a .sig tag. But that task's idempotency guard is
# `cosign verify && skip`, and cosign reads BOTH formats — so an image already
# signed as a referrer verifies, is skipped, and never gains a .sig tag.
# Those images need this one-off pass. Upgrading Kyverno (1.17/1.18 -> 1.19.0)
# and setting rekor.ignoreTlog did NOT help; the storage format is the blocker.
#
# WHAT IT DOES
# ------------
# Takes the images Kyverno is currently failing (i.e. exactly the images in use
# that lack a .sig tag), and re-signs each in legacy mode. Scope is deliberately
# "what the clusters actually reference" rather than "every tag in the
# registry" — the registry holds thousands of dead SNAPSHOT tags nobody needs
# verified.
#
# --tlog-upload=false is REQUIRED, not an optimisation. These digests are
# already in Rekor from the original signing; re-uploading returns HTTP 409
# ("an equivalent entry already exists in the transparency log") and would
# abort the run. The signature itself is unaffected — and our Kyverno policy
# sets rekor.ignoreTlog, so nothing consumes the log entry anyway.
#
# Re-signing is idempotent: it rewrites the same .sig tag for the same digest.
# Safe to re-run.
#
# USAGE
# -----
#   ./backfill-legacy-signatures.sh <kube-context>            # dry run (default)
#   ./backfill-legacy-signatures.sh <kube-context> --apply    # actually sign
#
# Requires: kubectl (context with read on kyverno PolicyReports + the
# cosign-keys secret in ns jx), cosign v3+, and registry push credentials for
# the registries involved (gcloud auth configure-docker / az acr login).
#
# NOTE: needs registry WRITE. Kyverno's own reader identity deliberately cannot
# do this — run it as an operator, not from in-cluster.

set -euo pipefail

CTX="${1:-}"
MODE="${2:---dry-run}"
if [ -z "$CTX" ]; then
  echo "usage: $0 <kube-context> [--apply]" >&2
  exit 2
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

echo "== context: $CTX  mode: $MODE"

# 1. The signing key, straight from the cluster it belongs to.
kubectl --context "$CTX" -n jx get secret cosign-keys \
  -o jsonpath='{.data.cosign\.key}' | base64 -d > "$WORK/cosign.key"
[ -s "$WORK/cosign.key" ] || { echo "could not read cosign-keys/cosign.key" >&2; exit 1; }
export COSIGN_PASSWORD=""

# 2. Images Kyverno currently cannot verify. Only "no signatures found" —
#    an auth error or a genuinely-unsigned image is a different problem and
#    must not be silently re-signed here.
kubectl --context "$CTX" get policyreport -A -o json \
| python3 -c '
import json,sys,re
d=json.load(sys.stdin); out=set()
for i in d.get("items",[]):
    for r in i.get("results",[]):
        if r.get("policy")!="verify-image-signatures": continue
        if r.get("result")!="fail": continue
        m=r.get("message","") or ""
        if "no signatures found" not in m: continue
        full=re.search(r"failed to verify image (\S+)(?=:\s*\.attestors)", m)
        if full: out.add(full.group(1))
for u in sorted(out): print(u)
' > "$WORK/images.txt"

TOTAL=$(wc -l < "$WORK/images.txt" | tr -d ' ')
echo "== images Kyverno reports as 'no signatures found': $TOTAL"
[ "$TOTAL" -gt 0 ] || { echo "nothing to backfill"; exit 0; }

# 3. Skip anything that genuinely has no signature at all (PR/SNAPSHOT builds
#    never went through cosign-sign). Re-signing those would be minting a new
#    attestation for an artefact that was never signed — a different and much
#    bigger decision than restoring a tag.
SIGNED="$WORK/signed.txt"; : > "$SIGNED"
UNSIGNED=0
while read -r IMG; do
  [ -n "$IMG" ] || continue
  if cosign verify --key "$WORK/cosign.key" --insecure-ignore-tlog=true "$IMG" >/dev/null 2>&1; then
    echo "$IMG" >> "$SIGNED"
  else
    UNSIGNED=$((UNSIGNED+1))
  fi
done < "$WORK/images.txt"

TOSIGN=$(wc -l < "$SIGNED" | tr -d ' ')
echo "== already signed (referrer) -> will backfill a .sig tag : $TOSIGN"
echo "== genuinely unsigned        -> SKIPPED, not our call    : $UNSIGNED"

if [ "$MODE" != "--apply" ]; then
  echo
  echo "-- dry run; would re-sign the following in legacy mode --"
  head -40 "$SIGNED"
  [ "$TOSIGN" -gt 40 ] && echo "   ... and $((TOSIGN-40)) more"
  echo
  echo "re-run with --apply to write .sig tags"
  exit 0
fi

OK=0; FAILED=0
while read -r IMG; do
  [ -n "$IMG" ] || continue
  if cosign sign --key "$WORK/cosign.key" --yes \
       --registry-referrers-mode=legacy --tlog-upload=false "$IMG" >/dev/null 2>&1; then
    OK=$((OK+1)); printf '\r  signed %d/%d' "$OK" "$TOSIGN"
  else
    FAILED=$((FAILED+1)); echo; echo "  FAILED: $IMG" >&2
  fi
done < "$SIGNED"
echo
echo "== done: $OK signed, $FAILED failed"
echo "Now force a Kyverno rescan and re-measure:"
echo "  kubectl --context $CTX -n kyverno rollout restart deploy/kyverno-reports-controller"
