# The chart standard, and how each rule is proven

A chart in this estate is not "a chart we lint". Every rule below exists because something in this
estate broke in exactly that way, and each one names **what proves it** — because a rule nobody can
test is a rule that holds until the first person who has not read this file.

Three tiers, deliberately separate. A chart can fail tier 1 and pass tier 3; they answer different
questions and the reports must not blur them.

| tier | question | runs where | fails the PR |
|---|---|---|---|
| **1 — neutral** | is this a valid, deployable chart at all? | render only | yes |
| **2 — Ship Proven standard** | does it follow *our* contract? | render + source | yes |
| **3 — the target** | would **this cluster** accept it, in this order? | live API server | no (report), then yes once the estate is clean |

The split matters because tiers 1 and 3 are portable — another cluster, another operator, another
GitOps tool — while tier 2 is ours. Mixing them produces a gate that cannot be reused and a report
nobody can act on.

## Tier 1 — neutral, no Ship Proven knowledge

Nothing below mentions `leartech-secrets`, a namespace we own, or a store of ours.

| check | proves |
|---|---|
| `helm dependency build` + `helm lint` | the chart parses, values satisfy the schema |
| render under **three** value sets: empty, `.Values` defaults, a full example | the chart has no accidental required-ness and no crash on empty |
| `kubeconform -strict` on the render | the manifests are valid against a real API schema, not just YAML |
| no object with an empty `metadata.name` after render | a value was missing and templated to nothing — the most common silent break |
| `tests/go.mod` present and its tests pass | **the chart has executable checks at all** (see "Requiring the proof" below) |

The last row is the one this estate lacks: `jx-build-cluster-gsm` has **35 charts and zero
`tests/`**. The catalog already ships a `chart-tests` step (`tasks/helm/pullrequest.yaml`) that
runs a repo's Go chart tests — but it treats `no tests/go.mod` as a pass with a note, so a repo
with no checks is indistinguishable from a repo whose checks passed.

## Tier 2 — the Ship Proven standard

### S1 — never name a store in a default, and no `| default` either

```yaml
# fails: renders "leartech-secrets" when nobody said so
{{- $store := .Values.clusterStore | default "leartech-secrets" }}
```

The store is a **required value, supplied from `config/secrets.yaml`**. A default hides the fact
that the chart was deployed without the value, and it hides it at exactly the moment the value is
wrong: `leartech-console-api`'s ExternalSecret defaults to `cluster-secret-store`, which does not
exist on this cluster, and the failure surfaces as `could not get secret data from provider` on an
object that reads correctly.

**Proven by a negative render:** `helm template` with the store value **unset** must FAIL. A chart
that renders without it has a default somewhere. This is why the check cannot be a static scan: the
only evidence that a value is required is that its absence breaks the render.

### S2 — `creationPolicy: Owner`

Every `ExternalSecret` sets it. `Merge`/`None` leaves an orphaned Secret behind when the ExternalSecret
is deleted, and an orphan is worse than a missing secret because the pod starts.

**Proven by:** render assert — every `kind: ExternalSecret` has `spec.target.creationPolicy == Owner`.

### S3 — one credential, one ExternalSecret, one namespace

Two objects must never claim the same Secret. This is the rule the estate just paid for: a chart
whose `creationPolicy: Owner` secret name is **a value** can collide with another chart that owns
the same name, and ESO's ownership then flips between reconciles — the secret churns, and whichever
consumer reads it mid-flip gets an empty value.

**Proven by:** render assert over the *whole repo* — collect every `ExternalSecret.spec.target.name`
(with its namespace) across all charts and fail on a duplicate. Also assert that no ExternalSecret
and no plain `Secret` in a chart share a name unless the file says why (`sp-proven.io/owners`
annotation), which is the shape `sp-infra-resources` already uses for the one legitimate
two-owner secret in the estate.

### S4 — a `template:` must enumerate all outputs

ESO **drops `spec.data` entirely** once `template.data` exists. So a `template` that names two of
three keys silently drops the third — no error, no empty field, the key is simply absent and the
consumer's own `required`/`?` catches it somewhere else.

**Proven by:** render assert — an ExternalSecret with `spec.template.data` must have a `spec.template`
whose keys cover the union of what its `data`/`dataFrom` produce, or must not carry `data` at all.
The simple, honest form: **never both `spec.data` and `spec.template.data` in one object** (no chart
in this estate needs both), and if `template.data` exists, assert its keys are non-empty.

### S5 — no secret material in values, including `todo` placeholders

`password: todo` is not a placeholder, it is a credential that will be deployed. The autogenerated
`jx-values.yaml` files carry `todo` for JX's own secrets; a chart of ours must not.

**Proven by:** source scan of `values.yaml`, `configs/`, and any `*.gotmpl` next to them, for a
`password|secret|token|clientSecret|apiKey` key with a literal value, plus a deny-list containing
`todo`, `changeme`, `xxx`.

### S6 — never set the Workload Identity annotation

```yaml
iam.gke.io/gcp-service-account: {{ .Values.serviceAccount.gcpServiceAccount }}
```

That annotation is set by **`terraform output`**, after the KSA exists, per cluster. A chart that
templates it puts a value in git that is only right for one cluster and one project, and the failure
is a permission error at runtime that reads like a code bug. Four charts in this repo do it today
(`blockchain-spike`, `ai-training-resources`, `ai-inference-resources`, `forge-keeper`).

**Proven by:** render assert — no rendered object carries `iam.gke.io/gcp-service-account`.

### S7 — fail loudly at start

A pod that starts without its credential and 401s every request is worse than one that crash-loops:
the rollout succeeds and the incident lands on whoever calls it. `leartech-auth-service`'s readiness
gate is the model.

**Proven by:** not renderable. It is a **chart test** (tier 1's `tests/go.mod`) asserting the
deployment mounts the secret with `optional: false`, or a live check that the pod is not Ready with
the secret removed. Design rules like this one are why tier 1 requires *executable* checks.

## Tier 3 — the target

Everything above is true of a chart sitting on a laptop. None of it answers "would **this** cluster
accept it, in this order". Two faults in `leartech-preview-infrastructure` were invisible to every
static tool: a migration Helm hook that deadlocked before its own PostgreSQL existed, and an http
issuer Hydra refuses at startup. Both rendered cleanly.

Tier 3 needs an engine that renders, orders and applies — `argocd-tools` (jx-build-cluster-gsm
#3174). Two checks, and they are the same command at different depths:

```
argocd app diff        --local <chart> --server ...   # rendered vs live: what would change
argocd app sync --dry-run --local <chart>             # would the API server ACCEPT it, in order
```

`--server-side --dry-run` also runs the admission chain, so **Kyverno decides** — which is how
`audit-run-as-nonroot` and the KES-denial policy become part of the PR rather than a surprise at
boot.

Two rules for this tier:

1. **It is opt-in by what the repo actually has.** If `preview/helmfile.yaml.gotmpl` exists, the repo
   deploys in CI, so run the dry-run against the preview namespace. If it does not, run against a
   scratch namespace or skip — and say which. A chart that never deploys is not proven by a
   dry-run into nowhere, and a check that quietly does nothing is the failure mode this estate
   keeps re-learning.
2. **Report first, gate later.** Run it on every PR and post the result; make it blocking only once
   the estate is clean, or the first PR it blocks is one nobody can fix in that PR.

## The console-api case, as the canonical fixture

`leartech-console-api` renders

```yaml
name: {{ .Values.database.externalSecret.storeRef | default "cluster-secret-store" }}   # S1
name: {{ .Values.database.secretName }}                                                # S3
creationPolicy: Owner                                                                  # S2 is honoured
```

Three rules in one file: the store falls back to a name that does not exist on this cluster, and the
secret name is a value, so an override can make it claim a Secret another chart already owns. It is
the first **tier-2 fixture** (`go/chartstd/testdata/console-api-default-store`) and the first
**tier-3 case**: a dry-run sync would have shown the collision at PR time, because the cluster
already holds the object.

## Requiring the proof

The catalog's `chart-tests` step already exists and runs a repo's Go chart tests. Two changes make it
a gate rather than a note:

- **no `tests/go.mod` fails** for a repo whose PR touches `charts/`, with the exemption written in
  the chart itself (`# chart-standard: no-tests <reason>`) rather than a global allow-list. The
  estate has 35 charts and 0 test suites; that number is the finding.
- the standard expects a **fixture per rule**: a chart that fails one rule for one reason. A rule
  whose failure has never been observed is a rule nobody has tested.


## Sibling gate: the status context a consumer declares

The chart standard judges a chart. This one judges the **chain from a status a PR
shows to the comment that explains it**, which lives across two repos:

```
consumer .lighthouse/jenkins-x/triggers.yaml  context: "audit"
  -> source audit.yaml --uses--> tasks/dotnet-audit/pullrequest.yaml
    -> that task posts nothing
```

Measured on `leartech-dotnet-service-template` (2026-11-18): **`pr`, `lint` and
`audit` are served by tasks that post no comment at all**, so a red one shows only
Lighthouse's generic "the following test failed" — the `console-api#31` shape,
generalised to three checks on every PR in that repo.

`go/triggersync` walks the three hops and reports:

| finding | means |
|---|---|
| context served by a task that posts under another `-check` | the comment exists and its marker names something no status carries |
| context served by a task that posts nothing | a red check with no explanation |
| duplicate context | two checks share one status; whichever finishes last decides the colour |
| source file missing | the status never runs, and reads as a failed check |
| `NOTE` (not a failure) | the task posts **inside its image** (`security-tools`, `ai-review`); unverified rather than absent |

Run it where the triggers file is: `triggersync -repo . -catalog <checkout>`. A
`NOTE` is deliberately not a failure — a checker that cries wolf is switched off
within a week, which is the lesson of every rule that had to be narrowed here.

### S8 — a boolean gate must not be written through sprig's `default`

```yaml
{{- if and .Values.database.enabled (.Values.database.externalSecret.enabled | default true) }}
```

`sprig`'s `default` returns the fallback when the value is **empty**, and `false` is
empty. So `database.externalSecret.enabled: false` evaluates to **true** and the object
renders anyway: the option is inexpressible. Measured on a three-line chart:

```
a: {{ if (.Values.enabled | default true) }}RENDERS{{ else }}SKIPPED{{ end }}   -> RENDERS
b: {{ if .Values.enabled }}RENDERS{{ else }}SKIPPED{{ end }}                    -> SKIPPED
c: {{ if ne (toString .Values.enabled) "false" }}RENDERS{{ else }}SKIPPED{{ end }} -> SKIPPED
```

Found in `leartech-console-api` (`mikelear/leartech-console-api#32`), where it cost
**three days of a whole world not syncing**: three places set `enabled: false` in the
belief it worked, and nothing could override the value from outside — `helm template -f`
with the value, a helmfile `set:`, and a dedicated values file all rendered unchanged.
The visible symptom was `secret "leartech-console-api-db" not found`, which reads as a
GSM problem.

**Proven by a source rule** (`-templates`), because the bug is invisible in the render the
author looked at: they set the value they believed in and the object agreed with the
default either way.

### T1/T2 — an off-switch must be proven by rendering it off

S8 catches the *spelling* of the bug in #32. It cannot catch the class: any chart
whose off-switch does nothing, whatever the reason. The only evidence that survives
an author believing the switch works is **rendering both ways and comparing**.

So a chart declares its switches next to itself:

```yaml
# charts/<chart>/chart-standard-toggles.yaml
toggles:
  - path: database.externalSecret.enabled
    off: "false"
    removes: ExternalSecret/leartech-console-api-db
    why: console-api#32 — the switch must actually remove it
```

the task renders `--set-string <path>=<off>` per toggle, and `chartstd` asserts:

| rule | finding |
|---|---|
| **T1** | the declared object is not in the DEFAULT render — the declaration is stale, or the default already has it off and nothing is proven |
| **T2** | the object is **still there** with the switch off — the value cannot express the off state (`console-api#32` verbatim) |
| **T3** (note) | an `*.enabled` key in `values.yaml` that no declaration covers: an off-switch nobody has flipped in a test |

T3 is deliberately a note. Every chart in this estate has undeclared switches, and
the useful first step is to make them **countable** rather than to red everything.
