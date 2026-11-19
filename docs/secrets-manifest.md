# The secrets manifest, and why a value's class is a rule

`bootstrap/secrets.yaml` in a GitOps repo declares every secret a cluster is seeded
with. `bootstrap/31-derive-secrets.sh` composes the ones that cannot be seeded. Neither
is a chart, so `chartstd` cannot see either, and both failures below were green
everywhere until a pod crashlooped.

## The classes

| mode | who may write it | what a wrong answer looks like |
|---|---|---|
| `copy` | the seeder, verbatim from the source project | a shared value drifting per cluster |
| `copy-template` | the seeder, rewriting the cluster name in the key | one cluster addressing another's secret |
| `generate` | the seeder once, create-if-absent, random | **rotating on re-run breaks a running database** |
| **`derived`** | **the derive script, composed from a generated value plus a cluster fact** | **random bytes in a URL** |
| `blank` | nobody yet — created empty and named as empty | silently absent, and the consumer waits |
| `manual` | a human | a value invented to make a gate green |

`generate` is create-if-absent on purpose: a re-run must not rotate a live password.
That means the seeder's own comment — *"composed by 31-derive-secrets.sh … seeding must
NOT invent one"* — is a rule, not a note.

## The rules

**V1** — closed vocabulary, one `why` per key, no duplicate key.
A mode outside the list is a typo or a new class, and both must fail a PR rather than a
cluster bootstrap. An unknown mode already makes the seeder exit at runtime; a PR is
cheaper than a bootstrap.

**V2** — a URL-shaped key must not be `generate`.
A key named `…-dsn`, `…-url`, `…-uri`, `…-connection`, `…-conn`, or declaring
`shape: dsn|url|uri`, is parsed by its consumer. Random bytes in it are a
**syntactically valid URL pointing at the right host with the wrong credential**:

```
console-api-db-dsn-ship-proven = "8f3a…" (64 hex)
  ExternalSecret  Ready=True
  pod             CreateContainerConfigError → CrashLoopBackOff
  message         unable to parse connection string
```

**V3** — a `derived` key must actually be derived, and declare its shape.
Declared `derived` but no line of the derive script writes it `→` the seeder skips it
(correctly), nothing composes it, and the consumer waits for a secret no step produces.
Without a `shape` nothing can assert the composed value is the kind the consumer parses.

**V4** — a key the derive script writes must be declared `derived`.
The other direction, and the one that hides: the script composes a value *while the
manifest still says `generate`*, so the seeder invents one first and the derive script
overwrites it. That works by **ordering**, not by design, and the ordering is not
written down anywhere.

## Where it runs

`valuesstd -manifest bootstrap/secrets.yaml -derive bootstrap/31-derive-secrets.sh`, on
the GitOps repo's PR, and in the image via `test/go/valuesstd_test.sh`.

## S9 — the mirror image of S6, and why it needs the live cluster

**S6** (chartstd) says nothing may *set* `iam.gke.io/gcp-service-account` in a chart: the
GSA is per-cluster and comes from `terraform output`. Correct — and it has a blind spot.
Once the annotation is removed from the chart it exists **only** in whatever last applied
it out of band, so a re-render recreates the ServiceAccount without it:

```
secret-infra/ServiceAccount external-secrets   annotation EMPTY
ClusterSecretStore                             still reports Valid — its check makes no live call
all 7 ExternalSecrets                          "could not get secret data from provider"
```

`identitystd -sa <kubectl dump> -require external-secrets=secret-infra` asserts the
**presence** side, against the live object, because no render contains it. It is the
cheapest honest instrument: it takes a dump rather than a kubeconfig, so the dump can be
a fixture and the invocation stays wherever the post-sync gate lives.

A ServiceAccount that does not exist is reported differently from one whose annotation
was stripped — same class, different fix.
