:white_check_mark: **End-to-end: PASS** `[gcp]` — 4/4 checks passed

Preview: https://thing-pr9.jx.leartech.com

| Test | Status | Duration | Message |
|---|---|---|---|
| `smoke` | :white_check_mark: pass | 503ms | OK |
| `audience` | :white_check_mark: pass | 765ms | A’s token refused by B, B’s refused by A |
| `auth-refusals` | :white_check_mark: pass | 110ms | OK |
| `suppliers` | :white_check_mark: pass | 142ms | OK |

<!-- leartech-end2end-gcp -->
```leartech-report
{
  "tool": "end2end",
  "check": "end2end",
  "outcome": "success",
  "cluster": "gcp",
  "summary": "4/4 checks passed",
  "tests": [
    {
      "name": "smoke",
      "status": "pass",
      "duration": "503ms",
      "message": "OK"
    },
    {
      "name": "audience",
      "status": "pass",
      "duration": "765ms",
      "message": "A’s token refused by B, B’s refused by A"
    },
    {
      "name": "auth-refusals",
      "status": "pass",
      "duration": "110ms",
      "message": "OK"
    },
    {
      "name": "suppliers",
      "status": "pass",
      "duration": "142ms",
      "message": "OK"
    }
  ],
  "version": "1"
}
```
