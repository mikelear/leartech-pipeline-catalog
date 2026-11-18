#!/bin/sh
# identitystd_test.sh — S9, the live half, run as a guard here.
#
# S6 says a chart must not SET the Workload Identity annotation. S9 says a
# ServiceAccount that NEEDS one must HAVE one, and the only place that is visible is
# the live object: on customer1 a re-render recreated the ESO ServiceAccount without
# its annotation, the ClusterSecretStore still reported Valid (its check makes no
# live call), and all seven ExternalSecrets failed with "could not get secret data
# from provider".
set -eu

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root/go/identitystd" || exit 1

command -v go >/dev/null || { echo "SKIP: no go toolchain in this image"; exit 0; }

echo "=== identitystd: a ServiceAccount that needs an identity must have one (S9) ==="
go test ./... || { echo "FAIL: identitystd's own suite is red"; exit 1; }

run() { go run . -sa "testdata/$1.json" -require external-secrets=secret-infra -require argocd-repo-server=argocd 2>&1 || true; }

out=$(run sa-stripped)
case "$out" in
  *secret-infra/external-secrets*) echo "  ok   sa-stripped names the object" ;;
  *) echo "FAIL: sa-stripped no longer reports the stripped annotation"; echo "$out" | sed 's/^/    /'; exit 1 ;;
esac

out=$(run sa-missing)
case "$out" in
  *"does not exist"*) echo "  ok   sa-missing distinguishes a missing object" ;;
  *) echo "FAIL: sa-missing no longer distinguishes a missing object"; exit 1 ;;
esac

out=$(run sa-good)
case "$out" in
  *FAIL*) echo "FAIL: sa-good is not clean"; echo "$out" | sed 's/^/    /'; exit 1 ;;
  *) echo "  ok   sa-good stays clean" ;;
esac

echo "=== ok ==="
