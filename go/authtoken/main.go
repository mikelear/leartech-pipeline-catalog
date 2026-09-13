// authtoken reads an OAuth2 token response or a JWT and prints one field of
// it, so end2end scripts can assert on tokens without a JSON parser.
//
// WHY THIS EXISTS. Five end2end scripts across four repos decoded JWTs with
// inline python3:
//
//	printf '%s' "$1" | python3 -c '
//	import sys, json, base64
//	t = sys.stdin.read().strip()
//	if t.count(".") < 2: print(""); raise SystemExit
//	p = t.split(".")[1]; p += "=" * (-len(p) % 4)
//	s = json.loads(base64.urlsafe_b64decode(p)).get("scp", [])
//	print(" ".join([s] if isinstance(s, str) else s))'
//
// That put a Python dependency in the path of every auth assertion this
// estate makes, in a Go shop, and made the end2end step apt-get install
// python3 before it could assert anything.
//
// Two subcommands, both reading stdin:
//
//	authtoken field access_token   # from a token response JSON
//	authtoken claim scp            # from a JWT
//
// A claim that is a JSON array prints space-separated, a string prints as
// is, and anything absent prints nothing — matching what the shell callers
// already compare against.
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: authtoken field <name> | authtoken claim <name>  (reads stdin)")
		os.Exit(2)
	}
	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "authtoken: reading stdin: %v\n", err)
		os.Exit(1)
	}

	var out string
	switch os.Args[1] {
	case "field":
		out, err = field(in, os.Args[2])
	case "claim":
		out, err = claim(in, os.Args[2])
	default:
		fmt.Fprintf(os.Stderr, "authtoken: unknown subcommand %q\n", os.Args[1])
		os.Exit(2)
	}
	if err != nil {
		// Print nothing on stdout and say why on stderr. The callers treat an
		// empty result as "not present", and their anchors abort on it -- so a
		// malformed token must not look like a token with no scopes, but it
		// must also not crash a suite mid-assertion.
		fmt.Fprintf(os.Stderr, "authtoken: %v\n", err)
		fmt.Print("")
		os.Exit(0)
	}
	fmt.Println(out)
}

// field returns a top-level string field of a JSON object.
func field(in []byte, name string) (string, error) {
	var m map[string]any
	if err := json.Unmarshal(in, &m); err != nil {
		return "", fmt.Errorf("input is not JSON: %w", err)
	}
	v, ok := m[name]
	if !ok {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("field %q is %T, not a string", name, v)
	}
	return s, nil
}

// claim returns one claim from a JWT's payload. The signature is not checked:
// what these scripts assert is what the issuer put in the token, and the
// resource server under test is the thing that verifies it.
func claim(in []byte, name string) (string, error) {
	t := strings.TrimSpace(string(in))
	parts := strings.Split(t, ".")
	if len(parts) < 3 {
		return "", fmt.Errorf("input has %d segment(s), so it is not a JWT", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Some issuers pad; RawURLEncoding does not accept padding.
		payload, err = base64.URLEncoding.DecodeString(parts[1] + strings.Repeat("=", (4-len(parts[1])%4)%4))
		if err != nil {
			return "", fmt.Errorf("payload is not base64url: %w", err)
		}
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return "", fmt.Errorf("payload is not JSON: %w", err)
	}
	switch v := m[name].(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case []any:
		var ss []string
		for _, e := range v {
			ss = append(ss, fmt.Sprint(e))
		}
		return strings.Join(ss, " "), nil
	default:
		return fmt.Sprint(v), nil
	}
}
