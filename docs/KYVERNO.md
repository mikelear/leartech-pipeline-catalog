# Kyverno — how it fits together here

Written 2026-09-14 after a long debugging session. Most of this is the
non-obvious half: the things that cost hours and are invisible from the docs.

## Three enforcement points, and you probably only have two

| point | what it does | where |
|---|---|---|
| Admission webhook | blocks (`Deny`) or records (`Audit`) at apply time | in-cluster, `failurePolicy: Fail` |
| Background scan | populates Policy Reporter | in-cluster, reports controller, **1h default** |
| CI / shift-left | fails the PR **before** merge | `tasks/kyverno-audit` |

**Do not add a CronJob to populate the dashboard.** Kyverno's reports
controller already background-scans on an interval; a CronJob duplicates it.
The interval is `backgroundScanInterval` (unset = 1h). To force a rescan:

```bash
kubectl -n kyverno rollout restart deploy/kyverno-reports-controller
```

## Use the new policy types. The old ones are deprecated and behave differently

`kyverno.io/v1 ClusterPolicy` is deprecated — the CLI warns on every load:

> kyverno.io/v1 ClusterPolicy is deprecated and will be removed in a future
> release; migrate to ValidatingPolicy, MutatingPolicy, GeneratingPolicy or
> ImageValidatingPolicy

This is not cosmetic. **OCI 1.1 referrer signatures are only readable by
`ImageValidatingPolicy`.** cosign v3 writes signatures as referrers by default,
so a legacy `ClusterPolicy.verifyImages` reports `no signatures found` for
images that are correctly signed. Proven on-cluster, same images and key:

```
legacy ClusterPolicy.verifyImages    1228 fail /   50 pass / 203 skip
ImageValidatingPolicy                   0 fail / 1098 pass
```

Things that did NOT fix that, so nobody repeats them:

* giving Kyverno a registry identity — necessary (it had none, every result was
  a 403) but it only turned 403s into `no signatures found`
* upgrading Kyverno 1.17/1.18 → 1.19.0 — worth doing anyway, it removed a real
  key-attestor crash, but the verdict did not change
* `rekor.ignoreTlog` on the legacy attestor
* `cosign sign --registry-referrers-mode=legacy` — that flag is a *fetch*
  option, inert on sign
* pinning cosign back to v2 — rejected; a security control should track current
  versions, not retreat

### Field names differ between the types

| legacy `ClusterPolicy` | new `ValidatingPolicy` / `ImageValidatingPolicy` |
|---|---|
| `validationFailureAction: Enforce` | `validationActions: [Deny]` — **`Enforce` is rejected** |
| `rekor.ignoreTlog` | `cosign.ctlog.insecureIgnoreTlog` |
| `match.any[].resources.kinds` | `matchConstraints.resourceRules[]` |
| autogen for Deployments is implicit | `autogen` is an explicit spec field |

`evaluation.background.enabled` must be **true** or the policy produces no
PolicyReports and the dashboard stays empty. The upstream examples set it
false.

Upstream examples also commonly validate only `images.containers`, which
leaves **initContainers unverified**. Check both.

## Only promote a policy to Deny after an audit shows it green

Run the audit (below), and promote only what already passes. From the
2026-09-14 audit, pods passing out of 1,400 across both clusters:

```
disallow-cri-sock-mount          1400   <- adopted at Deny
disallow-helm-tiller             1400   <- adopted at Deny
require-tekton-namespace-...      600/600 PipelineRuns  <- adopted at Deny
disallow-latest-tag              1340
require-requests-limits           870
drop-all-capabilities             370
require-pod-probes                326
require-ro-rootfs                 310
drop-cap-net-raw                    0   <- would stop the cluster dead
restrict-image-registries           0   <- placeholder allow-list, config it
require-labels                      -   <- BROKEN upstream, "no such overload"
```

`drop-cap-net-raw` is the cautionary tale: of the failures, **15 are Kyverno's
own pods** and 113 are in `jx`/`jx-git-operator` (boot jobs and every Tekton
build pod). With `failurePolicy: Fail` that is unrecoverable — nothing can
create pods, *including the boot job that would revert it*.

Always add a `namespaceSelector` excluding `kube-system`, `kube-node-lease`,
`kyverno` and `jx-git-operator`. Library policies ship no exclusions, so
Kyverno ends up policing itself and cannot recover from a bad rollout.

If admission ever wedges:

```bash
kubectl delete validatingwebhookconfiguration kyverno-resource-validating-webhook-cfg
```

then let boot reconcile it back.

Use **`PolicyException`** (both CRDs are installed) for per-workload carve-outs
rather than weakening a policy for everyone. That is what makes ratcheting a
fleet-wide policy practical.

## Running an audit locally

```bash
brew install kyverno                                  # match the cluster version
git clone --depth 1 https://github.com/kyverno/policies

kubectl get pods -A -o yaml > pods.yaml
# MUST split the List -- the CLI panics on it:
#   panic: *unstructured.Unstructured is not a list: no Items field
python3 -c "
import yaml
d=yaml.safe_load(open('pods.yaml'))
open('split.yaml','w').write(''.join('---\n'+yaml.safe_dump(i) for i in d['items']))"

kyverno apply policies/best-practices-vpol --resources split.yaml --policy-report > report.yaml
sed -n '/^apiVersion:/,$p' report.yaml > clean.yaml   # stdout is mixed with warnings
```

Only `best-practices-vpol`, `other-vpol`, `pod-security-vpol` and `psa-cel` are
current-generation in the upstream library. Everything else — including the
whole `tekton` set — is legacy `ClusterPolicy` and needs rewriting before use.

## jx-specific: apply does not prune

Boot runs `kubectl apply`; it never deletes. Replacing a policy leaves the old
one running and double-reporting, and immutable fields (`Job.spec.template`,
StatefulSet `volumeClaimTemplates`) cannot be patched at all — the apply fails
and the boot job goes red. Migrations need an explicit delete:

```bash
kubectl delete clusterpolicy <old-name>
```
