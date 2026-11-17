:x: **End-to-end: FAIL** `[gcp]` — the gate refused

- gate detail: timeout after 600s: http_probe: probe 'https://hydra-pr111.jx.leartech.com/.well-known/openid-configuration' returned 404

<!-- leartech-end2end-gcp -->
```leartech-report
{
  "tool": "end2end",
  "check": "end2end",
  "outcome": "failure",
  "cluster": "gcp",
  "summary": "preview-gate never reported ready within 10 min",
  "failures": [
    "gate detail: timeout after 600s: http_probe: probe 'https://hydra-pr111.jx.leartech.com/.well-known/openid-configuration' returned 404"
  ],
  "version": "1"
}
```
