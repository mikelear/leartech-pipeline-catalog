:x: **End-to-end: FAIL** `[gcp]` — 1/4 checks passed

| Test | Status | Duration | Message |
|---|---|---|---|
| `auth-refusals` | :x: fail | 110ms | python3: command not found |

- auth-refusals FAIL: python3: command not found — the runtime image carries jq and authtoken, not an interpreter

<!-- leartech-end2end-gcp -->
```leartech-report
{
  "tool": "end2end",
  "check": "end2end",
  "outcome": "failure",
  "cluster": "gcp",
  "summary": "1/4 checks passed",
  "failures": [
    "auth-refusals FAIL: python3: command not found — the runtime image carries jq and authtoken, not an interpreter"
  ],
  "tests": [
    {
      "name": "auth-refusals",
      "status": "fail",
      "duration": "110ms",
      "message": "python3: command not found"
    }
  ],
  "version": "1"
}
```
