# The hook standard — a hook is a hook on BOTH delivery paths

_This is the standard `go/argohooks` enforces. It exists because the same chart reaches a cluster in
one of two ways and the two disagree about what `helm.sh/hook` means._

## The two paths, measured

| | jx-boot cluster (build) | Argo CD cluster (customer2, every customer) |
|---|---|---|
| what applies the chart | real Helm, via `jx-git-operator` + helmfile | Argo's Helmfile CMP |
| `helm.sh/hook` | **honoured**: a `post-install` Job runs once, after install, then is deleted per its policy | **not implemented**: the object is an ordinary resource |
| when that object is applied | at the hook phase, once | during sync, at `sync-wave` 0, and **again on every later sync** |
| `helm.sh/hook-weight` | orders hooks | **inert** |
| `helm.sh/hook-delete-policy` | Helm deletes per the tokens | **inert** (Argo has its own annotation) |

One object, both live, same chart:

```
kyverno-migrate-resources      helm.sh/hook: post-upgrade
  build      created 2026-11-17T13:23Z    ran once, at that upgrade
  customer2  created 2026-11-23T10:56Z    Argo DELETED AND RECREATED it on sync
```

That is a migration Job whose schedule silently became "every sync". Nothing failed loudly. The cost
is invisible until the day the Job is not idempotent — and then it is a production incident whose cause
is in a chart nobody edited.

## The rule

**A hook is a hook on both paths, or it is not a hook.** Both annotations, always, and the Argo one
must be the documented *equivalent* — not merely present. A pair that is present but not equivalent is
worse than a missing one, because it reads as done.

| `helm.sh/hook` | `argocd.argoproj.io/hook` |
|---|---|
| `pre-install`, `pre-upgrade` | `PreSync` |
| `post-install`, `post-upgrade` | `PostSync` |
| `pre-delete` | `PreDelete` |
| `post-delete` | `PostDelete` |
| `test`, `test-success` | `Skip` — Argo **must not apply** a test Pod |
| `helm.sh/hook-weight N` | `argocd.argoproj.io/sync-wave: N` (same number; both scales run lower-first) |
| `before-hook-creation` | `BeforeHookCreation` |
| `hook-succeeded` | `HookSucceeded` |

## Three rows that are not "both, please" — they are "cannot be expressed"

| shape | why it cannot be translated | what to do |
|---|---|---|
| `helm.sh/hook-delete-policy: hook-failed` | Argo has no delete-on-failure; it **keeps** a failed hook. The failed Job stays, and the next sync sees a failing hook and blocks the Application | drop the token, handle the failure inside the Job |
| `test` / `test-success` mapped to anything but `Skip` | Argo applies the object, so a test Pod runs on every deploy, and a failing one blocks the sync | `Skip` |
| `pre-install` **and** `post-upgrade` on one object | under Helm those are two different moments; one object cannot carry two under Argo, and the combined form runs the work once per phase — **twice per sync** | split into one object per moment |

The helper and the checker both refuse these. A refusal at render time is the point: the alternative is
a chart that works on exactly one of the two clusters, and nobody knows which until it runs.

## How a chart does it — one declaration, not two

Use the library helper so the pair comes from ONE place and cannot drift:

```yaml
metadata:
  annotations:
    {{- include "leartech.labels" . | nindent 4 }}
    {{- include "leartech.hookAnnotations" (dict
          "phase"  "post-install,post-upgrade"
          "weight" -5
          "delete" "before-hook-creation,hook-succeeded") | nindent 4 }}
```

`leartech-helm-library/templates/_hooks.tpl` emits both sides, translates the weight to a sync-wave,
translates the delete policy, and **fails the render** on an untranslatable token.

## How a repo enforces it

```sh
helm template <chart> | argohooks -manifest -          # or: argohooks -manifest rendered/
```

`go/argohooks` reads what a chart **renders**, so the helper and a hand-written pair are checked the
same way. Rules:

| rule | fires when |
|---|---|
| `AH1` | a Helm hook with no Argo hook → ordinary resource under Argo (this is what all 28 hook-carrying objects in the estate did) |
| `AH2` | an Argo hook with no Helm hook → ordinary resource on jx-boot, so a migration can run before its database |
| `AH3` | the pair is present but not equivalent, or an unmappable phase, or two moments on one object |
| `AH4` | a delete-policy token with no Argo equivalent |
| `AH5` | a `hook-weight` with no matching `sync-wave` (ordering lost) |
| `AH6` | a test hook that is not `Skip` |
| `AH7` (warn) | two or more hooks in one phase with no weights — order undefined on both paths |

The tool's own tests run on every PR of this repo, and they assert **both directions**: a broken shape
is caught, and the same rule is **silent** on a correct chart. Each fixture asserts the *exact set* of
rules, so a check that starts crying wolf fails the build instead of quietly training people to ignore
it.

## What this standard does NOT cover

- **Whether a `PostSync` migration deadlocks.** `PostSync` runs after everything is Healthy, which is
  stricter than Helm's `post-install` — usually what you want, but a chart whose app cannot become
  healthy until the migration ran needs `PreSync` instead. That is a judgement per chart; a checker
  cannot see it.
- **A hook that is only rendered under a values branch.** The checker reads the render, so a branch
  that is off is a branch that is not checked. `chartstd`'s negative renders cover the shapes that
  matter.
