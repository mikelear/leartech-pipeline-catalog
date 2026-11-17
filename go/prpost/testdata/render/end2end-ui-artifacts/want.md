:x: **End-to-end UI: FAIL** `[gcp]` — 7/8 browser tests passed

Preview: https://leartech-auth-ui-pr111.jx.leartech.com

**Artifacts** (screenshots, videos, traces):

- :camera: [consent persists screenshot](https://storage.example/gcp/pr111/consent.png)
- :mag: [consent persists trace](https://storage.example/gcp/pr111/consent.zip)

```
[playwright] 8 tests, 1 failed
  expect(received).toBeVisible()
    the consent banner did not appear
```

<!-- leartech-end2end-ui-gcp -->
```leartech-report
{
  "tool": "end2end-ui",
  "check": "end2end-ui",
  "outcome": "failure",
  "cluster": "gcp",
  "summary": "7/8 browser tests passed",
  "version": "1"
}
```
