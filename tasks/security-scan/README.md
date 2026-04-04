# Security Scan Tasks

PR-triggered security scanning using `ghcr.io/mikelear/security-tools` (BlackArch image, all tools via pacman).

## Tasks

### `pullrequest.yaml` — Static Security Scan
- **Gitleaks**: scans working tree for leaked secrets
- **Semgrep**: static analysis with auto-config rules (SAST)
- Calls `/app/static-scan.sh` in the security-tools image
- Fails pipeline on leaked secrets or Semgrep errors

### `image-scan.yaml` — Dependency Vulnerability Scan
- **Grype**: scans repo dependencies (lockfiles, manifests) for CVEs
- Calls `/app/image-scan.sh` in the security-tools image
- Fails pipeline on critical vulnerabilities

### `dynamic/pullrequest.yaml` — Dynamic Preview Scan
- Deploys a scan pod **inside** the preview namespace
- **Nuclei**: SQL injection, XSS, SSRF, CVE detection against live preview URL
- **Nikto**: web server misconfiguration scanning
- **Nmap**: internal port scan (resolves service DNS directly)
- **Egress isolation**: applies NetworkPolicy deny, tests if app tries to phone home
- Calls `/app/dynamic-scan.sh` in the security-tools image
- Orchestrated by `jx-boot` image (has kubectl for namespace/pod/policy management)

## How It Works

```
App repo thin wrapper
  → uses: this catalog task
    → jx-boot step: wait for preview, apply NetworkPolicy, deploy scan pod
    → security-tools scan pod: /app/dynamic-scan.sh runs Nuclei+Nikto+Nmap
    → scan pod posts formatted PR comment with [cluster_id] tag
    → jx-boot step: cleanup scan pod + NetworkPolicy
```

## PR Comment Format

All scans post formatted markdown comments to the PR:

```
## :shield: Security Scan: **FAIL** `[gcp]`

| Scanner | Verdict | Findings |
|---|---|---|
| Gitleaks (secrets) | FAIL | 1 leaked secrets |
| Semgrep (SAST) | FAIL | 2 errors, 1 warnings, 0 info |
```

```
## :shield: Dynamic Security Scan: **Review Recommended** `[az]`

| Scanner | Findings | Status |
|---------|----------|--------|
| **Nuclei** (SQLi, XSS, SSRF, CVEs) | 0 critical, 0 high, 0 medium, 0 low | Pass |
| **Nikto** (Web Vulnerabilities) | 2 potential issues | Review |
| **Nmap** (Internal Port Scan) | 1 open ports (0 unexpected) | Info |
| **Egress Isolation** | 0 endpoints blocked | Pass |
```

## Wiring Into a Repo

See the main [README](../../README.md) for full setup instructions with example wrapper files and trigger config.
