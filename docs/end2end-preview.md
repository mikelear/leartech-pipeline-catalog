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
  setup:                        # optional; each entry is a verb from the catalog's menu
    - verb: probe
      url: http://preview-auth-service-hydra.${NAMESPACE}.svc:4445/.well-known/openid-configuration
      want: 200
      note: hydra's discovery document
    - verb: hydra-client
      name: thing-e2e
      scopes: [openid, offline, thing:read]
      audiences: [leartech-thing]
      redirectURIs: ["${PREVIEW_URL}/callback"]
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

**`setup`** — a closed menu, not a place to put scripts. See §3.

---

## 2. What the check enforces

`e2e contract -dir .` runs in the lint step. No cluster, no network, seconds.

| finding | why it exists |
|---|---|
| `healthPath` not declared | next-generation-lending-website#9: the wait guessed |
| `healthPath` not rooted (`health` not `/health`) | it is appended to a base URL |
| a `setup:` `probe` host no chart under `preview/` creates | leartech-auth-ui#111: every deployment ready, gate held closed on a 404'd hydra URL |
| a setup-named script nobody declared | `leartech-auth-service`'s reset script exists because a sibling repo's bespoke setup poisoned a shared fixture |
| a `setup:` verb that is not on the menu | four generations of the same hand-written client registration |
| a `bespoke:` entry with no reason, or pointed at staging | an exception nobody can weigh, on an environment another repo is using |
| a pack script that **requests** a host instead of the preview | a check that never mentions `PREVIEW_URL` cannot be about this PR |
| a pack script that is not executable | the other lane requires the bit; the move is when it breaks |
| an `END2END_*`/`E2E_*` env key no task reads | a repo believed it had declared a health path nothing read |
| `end2end/NONE` beside a live pack or contract | two authorities for one question |
| no pack and no `NONE` | the "undeclared" branch, made static |
| UI lane with no `*.spec.ts`, or a spec that never reads `PREVIEW_URL` | a green lane that tested nothing, or tested a literal host |

Every one of those is a real failure. That is the rule for adding a rule: **if the
estate has not paid for it, it is not a check.**

---

## 3. Setup is a menu, not a script

Every repo wrote its own setup: a `curl` at the Hydra admin API, a `kubectl exec` into
a sibling Postgres, a script that flipped a fixture user's role. Two costs, both paid.

**It duplicates.** Four generations of "register an OAuth client", each at a different
age, none of them read back after writing — so a client could be "seeded" and unable to
request the scope its suite asserts on.

**It can corrupt a shared environment.** `leartech-auth-service` carries
`end2end/00-reset-staging-fixtures.sh` whose header explains that a *sibling repo's*
suite flipped `test@` to `tenant_admin` and could not restore it, reddening a different
repo's gate until a redeploy re-seeded it. Its closing line is the standard: "staging
is a persistent, SHARED environment".

So the catalog provides the verbs and a repo names the verb its parameters:

```yaml
setup:
  - verb: hydra-client
    name: thing-e2e
    scopes: [openid, offline, thing:read]
    audiences: [leartech-thing]
    redirectURIs: ["${PREVIEW_URL}/callback"]
```

| verb | what it does | runnable today |
|---|---|---|
| `hydra-client` | register a client, **read it back**, assert scopes and audiences stuck | yes |
| `probe` | assert a URL answers a status | yes |
| `hydra-secret` | set a client secret from GSM and assert the client accepts it | declared |
| `auth-user` | create or assert a fixture user's role | declared |
| `scope` | declare a capability scope so the fleet's vocabulary agrees | declared |
| `audience` | declare an audience and assert a token can be minted for it | declared |

`e2e setup -dir .` prints the plan; `--apply` runs it. A verb that is declared but not
runnable **fails** rather than no-opping — a suite that silently ran without its
fixtures is worse than one that failed.

### `where`: preview and staging are not the same problem

| | preview | staging |
|---|---|---|
| lifetime | ephemeral, provisioned per PR | persistent, shared |
| who runs it | the PR's own pipeline | the **Observer**, on every dispatch |
| setup may | provision anything | only what is idempotent, because a sibling repo is using the same rows |

Every menu verb is idempotent by construction, so `where: staging` is safe. A bespoke
script is **preview only**, and the checker refuses `where: staging` on one — that is
the rule that would have prevented the poisoning incident above.

**The rule that follows for data:** a suite must not *depend* on staging having data
added for it. If staging needs fixtures, that is the deployment's job — the deploy that
populates staging runs once and asserts the rows exist; a test that inserts them is
doing deployment work on every run, against rows another repo is reading. So:

- **preview:** provision freely, it is yours.
- **staging:** assert, never insert. A `probe` or an `auth-user` verb is an assertion
  that the deployment did its job; a bespoke insert is not allowed there at all.

### The one legal exception

A procedure the menu cannot express is declared, with a reason, and stays preview-only:

```yaml
bespoke:
  - script: end2end/00-seed-session-store.sh
    reason: the Playwright login reads a cookie bound to the ingress hostname,
      which no menu verb can mint without a browser; replaced by a verb once one exists.
```

Undeclared → the check fails and names the file. Declared without a reason → fails.
Declared for staging → fails. Declared properly → passes, and `e2e explain` lists it, so
it cannot hide and a repo cannot accumulate three.

---

## 4. The examples are the regression pack

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
testdata/broken/bespoke-setup/          -> "the catalog does not provide"
testdata/broken/bespoke-in-staging/     -> "staging is persistent and shared"
testdata/broken/unknown-verb/           -> "not on the catalog's menu"
testdata/broken/bespoke-no-reason/      -> "has no `reason`"
```

The passing four are what a repo should look like. The broken five each reproduce a
failure a real PR had, and each must fail **for its own reason** — a checker that
rejected all five with one message would pass a looser test and teach nobody
anything.

So the documentation cannot drift: change a rule, and the example that demonstrates
it either still behaves as documented or the build fails.

---

## 5. Migrating a repo

1. Read what the task does today from the run you last had:
   `curl` the PR's end2end comment, or run `e2e explain` after writing step 2.
2. Write `end2end/preview.yaml` — start from the closest fixture in §3.
3. Set `gate:` honestly. If `preview/helmfile.yaml` has no `preview-gate` release,
   it is `absent`.
4. Convert any `0X-*.sh` that only sets state into a menu verb under `setup:`. If it
   genuinely is not expressible, declare it under `bespoke:` with a reason.
5. Add the `e2e contract` step to lint (one step; see `tasks/go-test` for the shape
   of a catalog step that curls a pinned tool).
6. Delete `END2END_HEALTH_PATH` from the pipeline YAML once `healthPath` is declared
   — the declaration wins, and two sources is how the original mismatch happened.

A repo is conformant when `e2e contract` is green and the end2end lanes no longer
have a readiness step that is longer than the suite.

---

## 6. What this changes about the failure messages

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

## 7. What the check says about the estate today

Run against all 34 repos that declare an `end2end` gate, with each repo's own
`.lighthouse`, `end2end/` and `preview/` trees materialised (470 files):

```
34 repos checked, 31 with findings

  31  no preview.yaml declared          <- every one of them, as expected: this is new
   4  pack script "pins a host"        <- ALL FOUR were expected-string comparisons
   1  no pack and no end2end/NONE      <- leartech-infra-agent
```

The first number is the migration (31 declarations to write, one PR each — §4). The
third is a real, tiny gap.

The second is worth recording as a lesson, because it is what the first version of
that rule got wrong. It flagged any literal URL in any line, and every one of the
four findings was a check comparing a response body against an expected string —
`"<loc>https://www.mortgagesourcing.com</loc>"` is not a request, it is an assertion
about a sitemap. The rule now only considers **request targets** (`curl`/`wget`
lines), and it reports **zero** findings across the same 34 repos. A rule that cries
wolf on correct code teaches people to ignore the gate, which is worse than not
having it.

So `hardcoded-host` in `testdata/broken/` is a rule guard, not a post-mortem: no PR
in the estate has hit it yet, and it is there so that the first one that does is
caught without a cluster.

## 8. Adoption, and why the check does not red the estate today

A repo that has not written `end2end/preview.yaml` has not adopted the standard, so the
check **reports** its findings and passes:

```
==> e2e contract: 1 finding(s) — NOT YET CONFORMANT (reported, not failed)
```

The moment the file exists, the same findings fail the gate. That boundary is one file
a reviewer can see, and it is the difference between a standard arriving and 31 repos
going red for a file that did not exist last week. `-strict` fails regardless, for a
repo that wants the gate before it has finished the declaration.

`leartech-auth-service` is the worked example: the first commit adds the declaration,
the check goes red naming its two bespoke scripts and telling it what to do; the second
declares them and it goes green.
