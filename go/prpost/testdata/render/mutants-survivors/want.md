### :white_check_mark: Stryker.NET `[gcp]` — 78% (78% (≥ 70% threshold))

**Outcomes**: killed=`142` survived=`3` no-coverage=`0` timeout=`1` (score 78%)

**Top surviving mutations** (gaps in test coverage):

| File | Line | Mutator |
|---|---|---|
| `Client.cs` | `==` → `!=` | Equality |
| `Guard.cs` | removed `if` | Statement |

<!-- leartech-mutants-gcp -->
```leartech-report
{
  "tool": "mutants",
  "check": "dotnet-mutants",
  "outcome": "success",
  "cluster": "gcp",
  "summary": "Mutation score 78% (\u003e= 70% threshold)",
  "version": "1"
}
```
