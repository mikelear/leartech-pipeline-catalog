:x: **some-new-check**: 2 of 5 sub-checks failed `[az]`

- a.go:12 unused variable
- b.go:40 missing error check

<!-- leartech-some-new-check-az -->
```leartech-report
{
  "tool": "some-new-check",
  "check": "new",
  "outcome": "failure",
  "cluster": "az",
  "summary": "2 of 5 sub-checks failed",
  "failures": [
    "a.go:12 unused variable",
    "b.go:40 missing error check"
  ],
  "version": "1"
}
```
