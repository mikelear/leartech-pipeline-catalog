:x: **End-to-end: FAIL** `[gcp]` — nothing ran

- This repo has neither an executable `end2end/run.sh` nor an `end2end/NONE` declaration, so nothing ran against the preview and nothing says that was intended.
- Pick one: add `end2end/run.sh` (chmod +x), or add `end2end/NONE` with one line saying why this service has no end2end suite.
- This used to report "PASS: no end2end configured", which made "no tests" and "all tests passed" the same green tick.

<!-- leartech-end2end-gcp -->
```leartech-report
{
  "tool": "end2end",
  "check": "end2end",
  "outcome": "failure",
  "cluster": "gcp",
  "summary": "no end2end/run.sh and no end2end/NONE declaration",
  "failures": [
    "This repo has neither an executable `end2end/run.sh` nor an `end2end/NONE` declaration, so nothing ran against the preview and nothing says that was intended.",
    "Pick one: add `end2end/run.sh` (chmod +x), or add `end2end/NONE` with one line saying why this service has no end2end suite.",
    "This used to report \"PASS: no end2end configured\", which made \"no tests\" and \"all tests passed\" the same green tick."
  ],
  "version": "1"
}
```
