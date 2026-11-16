import subprocess

msg = '''fix(end2end): the phase env files must EXPORT, not just assign

Found by running the standard against a real repo rather than a fixture. The migration
PR (leartech-auth-service) reached `end2end-run` with a healthy preview and then:

  [end2end] ---- logout-flow failed, full log ----
  end2end/03-logout-flow.sh: line 24: PREVIEW_URL: PREVIEW_URL must be set by the end2end task
  [end2end] SKIP dcr-discovery: neither PREVIEW_URL nor STAGING_URL set — skipping
  ... sixteen more skips ...
  2/5 checks passed, 16 skipped

The phases pass state through files they source. A plain assignment in a sourced file
sets a SHELL variable, which no child process inherits — and `end2end-run` runs the pack
as a child. Before the phase split all of this ran in one shell where the task's own
`export PREVIEW_URL` carried through, so the split lost the export while keeping the
value. Every phase's own logic worked; only the pack saw an empty environment.

That is the worst shape this class of bug takes: the harness reports 16 skips and 2/5
passed, which reads as "this repo's suite does not apply here" rather than "the runner
handed it nothing".

Every variable the files carry is now exported, and the first writer says why.
'''
subprocess.run(['git', 'add', '-A'], check=True)
subprocess.run(['git', 'commit', '-q', '-m', msg], check=True)
r = subprocess.run(['git', 'push', '-q', '-u', 'origin', 'fix/end2end-env-must-be-exported'],
                   capture_output=True, text=True)
print(r.stdout, r.stderr[-200:])
