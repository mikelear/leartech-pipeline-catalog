:x: **End-to-end UI: FAIL** `[gcp]` — 7/8 browser tests passed

Preview: https://leartech-auth-ui-pr111.jx.leartech.com

| Test | Status | Duration | Message |
|---|---|---|---|
| `login renders` | :white_check_mark: pass | 1.2s | OK |
| `consent persists` | :x: fail | 4.8s | expect(received).toBeVisible() — the consent banner did not appear |

<!-- leartech-end2end-ui-gcp -->
```leartech-report
{
  "tool": "end2end-ui",
  "check": "end2end-ui",
  "outcome": "failure",
  "cluster": "gcp",
  "summary": "7/8 browser tests passed",
  "tests": [
    {
      "name": "login renders",
      "status": "pass",
      "duration": "1.2s",
      "message": "OK"
    },
    {
      "name": "consent persists",
      "status": "fail",
      "duration": "4.8s",
      "message": "expect(received).toBeVisible() — the consent banner did not appear"
    }
  ],
  "version": "1"
}
```
