import pathlib

p = pathlib.Path('go/e2e/contract_check.go')
s = p.read_text()

anchor = '''	findings = append(findings, unknownEnvKeys(repoDir)...)'''
rules = '''	// ── the pack must not depend on a language the runtime does not carry ──
	//
	// 47 pack scripts across 18 repos called python3 to read a JSON field. That worked
	// while the end2end steps installed python3, and stopped the day the phases moved to
	// the catalog's alpine runtime — which is the runtime they SHOULD use, because it is
	// the one with the pinned tools. The failure is a "command not found" inside one
	// assertion, so the check reports the thing it could not read as the thing it
	// disproved: console-api#24 showed "auth-refusals FAIL" for a python3 that was not
	// there.
	//
	// jq is in that image, and so is authtoken (the catalog's own Go JWT/JSON reader), so
	// there is no procedure here that needs an interpreter the estate does not ship.
	for _, script := range scripts {
		body, err := os.ReadFile(script) // #nosec G304 -- path came from the pack read
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(body), "\\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") || !strings.Contains(trimmed, "python3") {
				continue
			}
			add("%s calls python3. The pack runs in the catalog's runtime image, which "+
				"carries jq and authtoken and no interpreter — so this line fails with "+
				"'command not found' and the check reports whatever it was about to assert "+
				"as disproved (leartech-console-api#24 read a missing python3 as 'auth "+
				"refusals broken'). For a JSON field: `... | jq -r .field`. For a token: "+
				"`authtoken field access_token`, `authtoken claim scp`, `authtoken payload`.",
				rel(repoDir, script))
			break
		}
	}

	// ── the audience probe must be declared, not assumed ──────────────────
	//
	// The cross-service audience property is "401 with a token minted for someone else,
	// 200 with its own" — so it needs ONE route per service, and it needs to be the same
	// kind of route everywhere for the comparison to mean anything. 27 pack scripts in 17
	// repos hardcode /api/v1/example, which a service is free not to expose. When it does
	// not, the control assertion ("this service accepts its OWN token") gets a 404 and
	// reports "CROSS-SERVICE AUDIENCE BINDING BROKEN" — a security finding, about correct
	// code. That is the failure mode this standard exists to remove, so the route is
	// declared once in preview.yaml and the pack reads $AUTH_PROBE.
	if c != nil {
		declared := c.AuthProbe
		for _, script := range scripts {
			body, err := os.ReadFile(script) // #nosec G304
			if err != nil {
				continue
			}
			text := string(body)
			if !strings.Contains(text, "/api/v1/example") {
				continue
			}
			if declared != "" && strings.Contains(text, "AUTH_PROBE") {
				continue // already reads the declaration
			}
			add("%s hardcodes the audience probe route /api/v1/example. A service does not "+
				"have to expose that route, and when it does not the control assertion gets "+
				"a 404 and the check reports 'cross-service audience binding broken' about "+
				"correct code. Declare the route the service DOES gate in preview.yaml as "+
				"`preview.authProbe`, and read $AUTH_PROBE in the script.", rel(repoDir, script))
		}
		if len(scripts) > 0 && declared == "" && anyHardcodesProbe(scripts) {
			// one finding for the declaration, and the per-file ones above say where.
			add("%s: preview.authProbe is not declared. The audience checks need one route "+
				"that is behind authentication, so that 'refuses a foreign token' can be "+
				"told apart from 'refuses everything'. Any authenticated GET will do — it "+
				"does not have to be a business route.", ContractFile)
		}
	}

	findings = append(findings, unknownEnvKeys(repoDir)...)'''

assert s.count(anchor) == 1
s = s.replace(anchor, rules, 1)

helper = '''
// anyHardcodesProbe reports whether any pack script names the shared probe path, so the
// declaration finding is reported once rather than per file.
func anyHardcodesProbe(scripts []string) bool {
	for _, s := range scripts {
		if b, err := os.ReadFile(s); err == nil && strings.Contains(string(b), "/api/v1/example") {
			return true
		}
	}
	return false
}

'''
s = s.replace('// looksLikeSetup reports whether a pack script', helper + '// looksLikeSetup reports whether a pack script', 1)
p.write_text(s)
print('rules added')
