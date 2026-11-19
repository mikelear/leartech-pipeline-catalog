#!/usr/bin/env bash
# Compute the secrets that CANNOT be seeded or randomised.
#
# THE LAYER THIS FIXES
# --------------------
# Seeding (30-secrets.sh) copies shared values and generates per-cluster ones. But some
# secrets are neither: they are ASSEMBLED from a generated value plus a Kubernetes DNS
# name. Generating them randomly produces a syntactically valid secret with a nonsense
# value, and the failure is a crash loop deep inside the component:
#
#   auth-hydra  Init:CrashLoopBackOff
#   auth-hydra-dsn-sp-infra = "8zn/Of0Q29NDcoOKUMgGKJ1aB54A/CIGmfLy2Zdk"   ← not a DSN
#
# Everything above it then waits: maester cannot reconcile the OAuth2Client CRDs, so the
# auth-service-internal-services / maestro-internal-services-auth / mcp-internal-services-oauth
# / jx-api-s2s / forge-lighthouse-secrets secrets are never produced, and 19 pods sit in
# CreateContainerConfigError waiting for them.
#
# THE PATTERN (verified against the build cluster, not guessed)
#   postgres://<user>:<password>@<service>:5432/<database>?sslmode=disable
# where <password> IS auth-postgresql-password-sp-infra (confirmed: the DSN's password
# equals that secret's value) and user=database=hydra (confirmed in
# helmfiles/sp-infra/configs/postgresql.yaml).
#
# Idempotent: recomputes and adds a new version only if the value would change.

# ---- cluster guard: the context is REQUIRED, never inferred -----------------
# Unified with leartech-sp-cluster's lib/cluster-guard.sh: a bootstrap script applies CRDs
# cluster-wide and patches the Argo deployment, so "whichever cluster kubectl points at" is
# not an acceptable target. See leartech-sp-cluster 00-investigation/PHASES.md - the ladder these scripts are phase 4 of.
# shellcheck source=./_cluster.sh
source "$(dirname "$0")/_cluster.sh"
require_context "$@" || exit $?

set -euo pipefail
cd "$(dirname "$0")/.."

# RUN ORDER: LAST of the five. It composes values from Secrets that only exist once the cluster is
# running (a Postgres password, a mongo credential), so kubectl must point at THIS cluster first.
# Everything before it is declarative; this one reads live state.

PROJECT="${PROJECT_ID:-${1:-}}"
[ -n "$PROJECT" ] || { echo "ERROR: set PROJECT_ID or pass the project as \$1" >&2; exit 1; }

get() { gcloud secrets versions access latest --secret="$1" --project="$PROJECT" 2>/dev/null; }

setsecret() {
  local key="$1" value="$2"
  local current
  current="$(get "$key" || true)"
  if [ "$current" = "$value" ]; then
    printf '  unchanged  %s\n' "$key"
    return 0
  fi
  gcloud secrets describe "$key" --project="$PROJECT" >/dev/null 2>&1 \
    || gcloud secrets create "$key" --project="$PROJECT" --replication-policy=automatic >/dev/null
  printf '%s' "$value" | gcloud secrets versions add "$key" --project="$PROJECT" --data-file=- >/dev/null
  printf '  derived    %s\n' "$key"
}

# The Postgres password is the input for every DSN. If 30-secrets.sh has not run there is nothing
# to compose from, and the failure would otherwise be a cryptic empty string in a URL.
probe="$(gcloud secrets versions access latest --secret=auth-postgresql-password-sp-infra --project="$PROJECT" 2>/dev/null || true)"
if [ -z "$probe" ]; then
  echo "ERROR: auth-postgresql-password-sp-infra is absent in $PROJECT." >&2
  echo "       Run bootstrap/30-secrets.sh first — this script composes FROM the generated values." >&2
  exit 1
fi

echo "deriving composed secrets in $PROJECT"

# The password the Postgres chart is told to use for the hydra user. One value, so every
# DSN must reference this — not a second generated password.
PW="$(get auth-postgresql-password-sp-infra)"
[ -n "$PW" ] || { echo "ERROR: auth-postgresql-password-sp-infra is absent — run 30-secrets.sh first" >&2; exit 1; }

# PER NAMESPACE. sp-infra and sp-plan-infra are two separate worlds, each with its own
# Postgres service and its own generated password — so the DSN is built per namespace,
# not once. Getting this wrong is invisible until the second namespace's Hydra crashloops
# with "dsn value requested an unknown driver" (which is what a non-DSN value produces).
for world in sp-infra sp-plan-infra; do
  pw="$(get "auth-postgresql-password-${world}" || true)"
  if [ -z "$pw" ]; then
    printf '  SKIP       %s (no auth-postgresql-password-%s yet)\n' "$world" "$world"
    continue
  fi
  # PERCENT-ENCODE the password. A '/' inside it terminates the URL authority, and the
  # DSN then parses as user=<user>, NO password, database=<rest of the password>:
  #
  #   "failed to connect to `user=nobody database=bStX18qJaK7cdDQoEcZXEUlCj81xJj5YLuAN6stEFU=@auth-postgresql:5432/hydra`"
  #
  # Hydra retries that forever, and it is luck-of-the-draw: sp-infra's generated password
  # had no '/' and started fine; sp-plan-infra's did and crashlooped. 30-secrets.sh now
  # generates hex (URL-safe), but encoding here also repairs values already issued.
  pw_enc="${pw//\//%2F}"
  pw_enc="${pw_enc//+/%2B}"
  dsn="postgres://hydra:${pw_enc}@auth-postgresql:5432/hydra?sslmode=disable"
  setsecret "auth-hydra-dsn-${world}"      "$dsn"
  setsecret "auth-service-db-dsn-${world}" "$dsn"
done

# ---- MAESTRO MONGO ----
# NO USERINFO IN THE URL, because the mongo chart runs with `auth.enabled: false`.
#
# bitnami/mongodb with auth disabled creates NO users — not even root — so any URL carrying
# user:password@ is rejected:
#
#   mongosh 'mongodb://maestro:<pw>@auth-mongodb:27017/Mqube-Maestro?authSource=Mqube-Maestro'
#     -> MongoServerError: Authentication failed
#   mongosh 'mongodb://auth-mongodb:27017/Mqube-Maestro'                               -> ok
#
# Verified both ways on this cluster. The BUILD cluster accepts the credentialled form only
# because its PVC predates auth being turned off: the maestro user still exists in its data
# directory. A fresh cluster has no such history, so the inherited URL cannot work — which is
# why this only shows up on a new customer cluster.
for world in sp-infra sp-plan-infra; do
  case "$world" in
    sp-infra) world_suffix="" ;;
    *)        world_suffix="-${world}" ;;
  esac
  setsecret "sp-infra-maestro-mongodb${world_suffix}" \
    "mongodb://auth-mongodb.${world}.svc.cluster.local:27017/Mqube-Maestro?authSource=Mqube-Maestro"
done

# NOTE: leartech-ship-proven-umbrella-leartech-console-api-database-url is NOT derivable
# from anything that exists — it is absent from the source project too, so it needs a
# decision about which database console-api uses. Left alone deliberately rather than
# filled with a plausible-looking wrong value.

echo
echo "done. Hydra should start, then maester reconciles the OAuth2Client CRDs and the"
echo "remaining auth secrets appear on their own."
