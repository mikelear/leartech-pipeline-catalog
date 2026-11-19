#!/usr/bin/env bash
get() { gcloud secrets versions access latest --secret="$1" --project="$P" 2>/dev/null; }
PW="$(get auth-postgresql-password-sp-infra)"
set_secret auth-hydra-dsn-sp-infra "postgres://hydra:${PW}@auth-postgresql:5432/hydra?sslmode=disable"
