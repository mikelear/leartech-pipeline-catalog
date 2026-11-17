:x: **End-to-end: FAIL** `[gcp]` — the wait failed

Preview: https://leartech-console-api-pr24.jx.leartech.com

- http://preview-thing.jx-pr24.svc:8080/health never returned 3 consecutive 2xx within 10m0s (last: HTTP 404)
- If the app serves a different path, set preview.healthPath in end2end/preview.yaml

<!-- leartech-end2end-gcp -->
```leartech-report
{
  "tool": "end2end",
  "check": "end2end",
  "outcome": "failure",
  "cluster": "gcp",
  "summary": "preview never reached stable ready state",
  "failures": [
    "http://preview-thing.jx-pr24.svc:8080/health never returned 3 consecutive 2xx within 10m0s (last: HTTP 404)",
    "If the app serves a different path, set preview.healthPath in end2end/preview.yaml"
  ],
  "version": "1"
}
```
