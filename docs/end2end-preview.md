# The end2end standard: declare your preview, then check it in PRs

Everything an end-to-end gate needs to know about a preview is knowable **before a
PR is opened**. This document is the standard for saying it, and the checker that
enforces it.

Both are new. The standard is the answer to a measured problem: across the estate,
**19 of ~30 end2end failures were the harness failing to agree with itself about
whether a preview was up** — not the product failing. Two lanes probed the same
preview, at the same moment, and disagreed.

```
<!-- leartech-end2end-gcp -->        0/2 checks passed, GET / -> 404   (it reached the app)
<!-- leartech-end2end-ui-gcp -->     /health never returned 200       (it did not)
```

Nothing in either message said which of the two was wrong.

---

## 1. What a repo declares

One file, beside the pack: **`end2end/preview.yaml`**.

```yaml
preview:
  healthPath: /health            # required; the path readiness is judged on
  service:    leartech-sp-web    # the workload under test
  port:       80                 # optional; else the Service's first port, else 80
  gate:       auto              # auto | required | absent
  versionAnchor: true           # optional; default true
  probes:                       # optional; must pass before any check runs
    - url: http://preview-auth-service-hydra.${NAMESPACE}.svc:4445/.well-known/openid-configuration
      want: 200
      note: hydra's discovery document
  seeds:                        # optional; SETUP only, never checks
    - end2end/00-seed-test-data.sh
ui:
  probe: ingress               # ingress | service (UI lane only)
```

Run `e2e explain -dir .` to see it read back **with every default resolved**. A
declaration you cannot read back is one you cannot review.

### The four keys that matter

**`healthPath`** — the single path readiness is judged on. It is required, and its
absence is a finding, because the alternative is the guess that failed
`next-generation-lending-website#9`: a repo set `END2END_HEALTH_PATH=/health`, the
bash lane probed `/health/live`, and the PR was failed for a preview that was serving.

**`service`** — the workload under test. A preview namespace also runs its own
infrastructure. Leaving this out means the wait cannot choose, and choosing wrong is
not hypothetical: `leartech-auth-service`'s preview runs Hydra, and taking
`items[0]` picked Hydra's `/health/alive` and then probed the app with it.

**`gate`** — whether the preview ships `preview-gate`.

| value | meaning |
|---|---|
| `auto` (default) | probe once, briefly; use it if it answers |
| `required` | the repo *relies* on the gate's extra probes; not deployed = fail fast, because falling back would test something weaker than was asked for |
| `absent` | do not probe at all — this is the one that removes the wait entirely |

39 of the 41 repos with a `preview/` directory do not deploy the gate, and every one
of their runs spends 60–180 seconds discovering that. `gate: absent` says it once.

**`seeds`** — setup, listed explicitly. The UI lane currently runs **every**
`0X-*.sh` as a "seed" with failures swallowed; on `leartech-auth-service` that is 21
scripts including the whole functional suite, so a real failure is printed as
"(seed script exited non-zero — continuing)". A seed's name says what it sets up
(`register`, `seed`, `setup`, `reset`, `fixture`, `bootstrap`); anything else is a
check and belongs in the pack.

---

## 2. What the check enforces

`e2e contract -dir .` runs in the lint step. No cluster, no network, seconds.

| finding | why it exists |
|---|---|
| `healthPath` not declared | next-generation-lending-website#9: the wait guessed |
| `healthPath` not rooted (`health` not `/health`) | it is appended to a base URL |
| a `probes:` host no chart under `preview/` creates | leartech-auth-ui#111: every deployment ready, gate held closed on a 404'd hidra URL |
| a `seeds:` entry that looks like a check | the auth-service suite ran twice as "seeding" |
| a `seeds:` entry that is missing or not executable | the seed loops skip silently |
| a pack script with a literal host | leartech-mortgagesourcing-web#18: smoke hit a public host |
| a pack script that is not executable | the other lane requires the bit; the move is when it breaks |
| an `END2END_*`/`E2E_*` env key no task reads | a repo believed it had declared a health path nothing read |
| `end2end/NONE` beside a live pack or contract | two authorities for one question |
| no pack and no `NONE` | the "undeclared" branch, made static |
| UI lane with no `*.spec.ts`, or a spec that never reads `PREVIEW_URL` | a green lane that tested nothing, or tested a literal host |

Every one of those is a real failure. That is the rule for adding a rule: **if the
estate has not paid for it, it is not a check.**

---

## 3. The examples are the regression pack

`go/e2e/testdata/` holds declarations, and `go/e2e/testdata_test.go` runs the checker
over all of them on every build:

```
testdata/go-service/     a Go API: seed, smoke, an auth'd check that exits 77 when
                         it does not apply
testdata/web-app/        a static site: /health served by nginx, gate absent
testdata/ui-app/         both lanes, Playwright specs that read PREVIEW_URL
testdata/opted-out/      end2end/NONE, and nothing else required of it

testdata/broken/undeclared-health/      -> "healthPath"
testdata/broken/wrong-probe-host/       -> "no chart under preview/"
testdata/broken/seed-that-is-a-check/   -> "looks like a CHECK"
testdata/broken/hardcoded-host/         -> "pins the host"
testdata/broken/typo-env/               -> "END2END_HEALTH_PAHT"
```

The passing four are what a repo should look like. The broken five each reproduce a
failure a real PR had, and each must fail **for its own reason** — a checker that
rejected all five with one message would pass a looser test and teach nobody
anything.

So the documentation cannot drift: change a rule, and the example that demonstrates
it either still behaves as documented or the build fails.

---

## 4. Migrating a repo

1. Read what the task does today from the run you last had:
   `curl` the PR's end2end comment, or run `e2e explain` after writing step 2.
2. Write `end2end/preview.yaml` — start from the closest fixture in §3.
3. Set `gate:` honestly. If `preview/helmfile.yaml` has no `preview-gate` release,
   it is `absent`.
4. Move any `0X-*.sh` that only sets state into `seeds:`, and rename it for what it
   sets up.
5. Add the `e2e contract` step to lint (one step; see `tasks/go-test` for the shape
   of a catalog step that curls a pinned tool).
6. Delete `END2END_HEALTH_PATH` from the pipeline YAML once `healthPath` is declared
   — the declaration wins, and two sources is how the original mismatch happened.

A repo is conformant when `e2e contract` is green and the end2end lanes no longer
have a readiness step that is longer than the suite.

---

## 5. What this changes about the failure messages

The wait now names the deployment, the URL and the status, and it quotes the gate's
own message on timeout — because the gate's JSON was already the best diagnosis in
the system:

```
deployment: preview-leartech-sp-web  1/1 ready  image leartech-sp-web:0.0.0-PR-22-2-SNAPSHOT
healthPath: /health   [declared]
base: http://leartech-sp-web.jx-pr-22.svc.cluster.local:80   (the app's Service, not the ingress)
version: 0.0.0-PR-22-2-SNAPSHOT matches
gate: auto, not deployed
probe: 1/3 ok … 3/3 ok
probe: ok http://hydra.jx-pr-22.svc:4445/.well-known/openid-configuration -> 200
verdict: READY
```

and on a failure, the same trace plus the reason — never "never returned 3
consecutive 200s" on its own.

`e2e rehearse -dir . -namespace <ns>` prints exactly that trace against a preview
that already exists, using the same code CI runs. It is what a developer runs
instead of opening a PR to find out.
