#!/bin/sh
# One place reads the cluster's identity, and this is what keeps it one place.
#
# The read is go/spvariables (clusterValues): it reads jx-cluster-config inside the binary, so
# the sp-variables STEP declares no env at all, and every later step gets the value from
# .jx/variables.sh. 34 task files predate that and still declare a configMapKeyRef; the list in
# test/go/clusterrefs.txt records them, with the reason each survives.
#
# The ratchet, both ways, because both silent failures have happened in this tree before:
#   - a task that declares the keyRef and is NOT listed fails, so the count cannot grow;
#   - a listed file that no longer declares it fails, so the list cannot go on describing a
#     tree that is not there (the defect test/go/orphan_tools.txt records five of).
set -eu
cd "$(dirname "$0")/../.."

list=test/go/clusterrefs.txt
recorded=$(mktemp)
actual=$(mktemp)
trap 'rm -f "$recorded" "$actual"' EXIT

grep '^tasks/' "$list" | cut -d'|' -f1 | sort > "$recorded"
grep -rl 'name: jx-cluster-config' tasks | sort > "$actual"

fail=0

for f in $(grep -vxF -f "$recorded" "$actual" || true); do
  echo "FAIL: $f declares a jx-cluster-config keyRef and is not in $list."
  echo "      The cluster identity is read in ONE place (go/spvariables). Use the value from"
  echo "      .jx/variables.sh, or add this file to the list with a reason it still needs it."
  fail=1
done

for f in $(grep -vxF -f "$actual" "$recorded" || true); do
  echo "FAIL: $list lists $f, which no longer declares the keyRef. Delete the stale line."
  fail=1
done

if [ "$fail" -eq 0 ]; then
  echo "==> one cluster-config read: sp-variables declares none, $(wc -l < "$recorded" | tr -d ' ') tasks still do (recorded in $list, may only shrink)"
fi
exit "$fail"
