:x: **End-to-end: FAIL** `[gcp]` — 1/3 checks passed, 1 skipped

Preview: https://leartech-console-api-pr24.jx.leartech.com

| Test | Status | Duration | Message |
|---|---|---|---|
| `smoke` | :white_check_mark: pass | 628ms | OK |
| `fleet-test` | :large_blue_circle: skip | 2ms | STAGING_URL unset, so this is a PR/preview context. This check is staging-only. |
| `cross-service-audience` | :x: fail | 765ms | CROSS-SERVICE AUDIENCE BINDING BROKEN |
| `auth-refusals` | :x: fail | 110ms | 05-auth-refusals.sh: line 105: python3: command not found |

<details><summary>What this run measured</summary>

### Cross-service audience binding

| token minted for | at `preview` | at `preview-peer` |
|---|---|---|
| `leartech-console-api` | **200** | **401** |

</details>

<!-- leartech-end2end-gcp -->
```leartech-report
{
  "tool": "end2end",
  "check": "end2end",
  "outcome": "failure",
  "cluster": "gcp",
  "summary": "1/3 checks passed, 1 skipped",
  "metrics": {
    "failed": "2",
    "passed": "1",
    "skipped": "1"
  },
  "tests": [
    {
      "name": "smoke",
      "status": "pass",
      "duration": "628ms",
      "message": "OK"
    },
    {
      "name": "fleet-test",
      "status": "skip",
      "duration": "2ms",
      "message": "STAGING_URL unset, so this is a PR/preview context. This check is staging-only."
    },
    {
      "name": "cross-service-audience",
      "status": "fail",
      "duration": "765ms",
      "message": "CROSS-SERVICE AUDIENCE BINDING BROKEN"
    },
    {
      "name": "auth-refusals",
      "status": "fail",
      "duration": "110ms",
      "message": "05-auth-refusals.sh: line 105: python3: command not found"
    }
  ],
  "version": "1"
}
```
