import pathlib

p = pathlib.Path('go/e2e/contract_check.go')
s = p.read_text()

anchor = '''	// ── the pack must not depend on a language the runtime does not carry ──'''
rule = '''	// ── every request in the pack must be bounded ─────────────────────────
	//
	// A curl with no `-m`/`--max-time` has no upper bound: against a preview that is
	// answering slowly, or a Service whose endpoints are empty, it waits until the kernel
	// gives up — and the runner cannot tell a hang from a slow suite, so the run ends on
	// the PIPELINE timeout with nothing in the log. leartech-auth-service's 30-minute
	// PipelineRun timed out exactly this way, having executed no assertions, because 102
	// of its curls were unbounded.
	//
	// A timeout is the difference between "this failed" and "this never answered", and a
	// suite that cannot tell them apart cannot report either.
	for _, script := range scripts {
		body, err := os.ReadFile(script) // #nosec G304 -- path came from the pack read
		if err != nil {
			continue
		}
		lines := strings.Split(string(body), "\\n")
		for i, line := range lines {
			if !strings.Contains(line, "curl ") || strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			// The flags may be on a following continuation line, so look at the whole
			// invocation: this line plus any that continue it.
			window := line
			for j := i + 1; j < len(lines) && j <= i+4; j++ {
				window += " " + lines[j]
				if !strings.HasSuffix(strings.TrimSpace(lines[j-1]), "\\\\") {
					break
				}
			}
			if boundedRe.MatchString(window) {
				continue
			}
			add("%s:%d has a curl with no timeout. It has no upper bound: against a slow "+
				"preview, or a Service with no endpoints, it waits until the kernel gives up, "+
				"and the run then dies on the pipeline timeout with nothing in the log to say "+
				"which check hung. Add -m 20 (or --max-time) to the invocation.",
				rel(repoDir, script), i+1)
			break
		}
	}

''' + anchor

assert s.count(anchor) == 1
s = s.replace(anchor, rule, 1)

# the compiled pattern, next to the other regexes
s = s.replace('''var (
	curlURLCall = regexp.MustCompile''','''var (
	// boundedRe matches the flags that give curl an upper bound, including the
	// connect-timeout variants: any of them means the call cannot wait forever.
	boundedRe   = regexp.MustCompile(`-m[ =]?[0-9]|--max-time|--connect-timeout|-y[ =]?[0-9]`)
	curlURLCall = regexp.MustCompile''')

p.write_text(s)
print('unbounded-curl rule added')
