:hourglass_flowing_sand: **Dynamic security scan: RUNNING** `[gcp]`

The preview did not become ready within 900s, so no scan ran. Re-run with `/test dynamic-security-scan` once it is deployed.

Nikto + Nuclei did not execute.

<!-- leartech-dynamic-scan-gcp -->
```leartech-report
{
  "tool": "dynamic-scan",
  "check": "dynamic-scan",
  "outcome": "pending",
  "cluster": "gcp",
  "summary": "skipped: preview not ready in 900s",
  "version": "1"
}
```
