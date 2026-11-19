p = 'go/chartstd/tenant.go'
s = open(p).read()

old = """				if notATenant.MatchString(strings.TrimSpace(val)) {
					continue
				}"""
new = """				// An empty value is the CORRECT form (the deploy supplies the
				// project); YAML has already unquoted `""`, so the regex cannot
				// see it. Caught by the tests: tenant-clean was reported.
				if strings.TrimSpace(val) == "" || notATenant.MatchString(strings.TrimSpace(val)) {
					continue
				}"""
assert old in s, "guard not found"
open(p, 'w').write(s.replace(old, new, 1))
print("empty string is not a tenant")
