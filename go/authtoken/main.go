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
//	authtoken payload              # a JWT's whole payload, as JSON
//	authtoken forge                # claims JSON in, an unverifiable JWT out
//	authtoken count                # how many entries a JSON array has
//	authtoken pick users email=x id  # one object's field, found by another
//	authtoken urldecode            # form-encoded text in, plain text out
//
// A claim that is a JSON array prints space-separated, a string prints as
// is, and anything absent prints nothing — matching what the shell callers
// already compare against.
package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) < 2 || len(os.Args) > 5 {
		fmt.Fprintln(os.Stderr, "usage: authtoken field <name> | claim <name> | payload | forge | "+
			"count | pick <array> <key>=<value> <field> | urldecode  (reads stdin)")
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
		out, err = field(in, arg(2))
	case "claim":
		out, err = claim(in, arg(2))
	case "payload":
		out, err = payload(in)
	case "forge":
		out, err = forge(in)
	case "count":
		out, err = count(in)
	case "pick":
		out, err = pick(in, arg(2), arg(3), arg(4))
	case "urldecode":
		out, err = urldecode(in)
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
	b, err := decodePayload(in)
	if err != nil {
		return "", err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
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

func arg(i int) string {
	if len(os.Args) > i {
		return os.Args[i]
	}
	fmt.Fprintf(os.Stderr, "authtoken: %s needs a name\n", os.Args[1])
	os.Exit(2)
	return ""
}

// payload returns a JWT's payload as compact JSON, for callers that want to
// read several claims from one decode.
func payload(in []byte) (string, error) {
	b, err := decodePayload(in)
	if err != nil {
		return "", err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return "", fmt.Errorf("payload is not JSON: %w", err)
	}
	out, err := json.Marshal(m)
	return string(out), err
}

// forge reads a claims object and returns a syntactically valid JWT that no
// issuer can have signed: HS256 over a key from crypto/rand, discarded
// immediately. Nothing in the estate holds that key, so the signature cannot
// verify however correct the claims look -- which is the point. A suite needs
// this to show that a resource server checks signatures rather than merely
// parsing tokens, and "reject this" is only meaningful if the rejection
// cannot be about the claims.
//
// The key is never printed. A forged token that could be verified by anything
// would be a credential, not a fixture.
func forge(in []byte) (string, error) {
	var claims map[string]any
	if err := json.Unmarshal(in, &claims); err != nil {
		return "", fmt.Errorf("claims input is not JSON: %w", err)
	}
	if len(claims) == 0 {
		return "", fmt.Errorf("no claims given; a forged token with an empty payload " +
			"would be rejected for its claims rather than its signature")
	}
	hdr, err := json.Marshal(map[string]any{"alg": "HS256", "typ": "JWT", "kid": "forged"})
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	signing := enc.EncodeToString(hdr) + "." + enc.EncodeToString(body)

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("generating a throwaway key: %w", err)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(signing))
	return signing + "." + enc.EncodeToString(mac.Sum(nil)), nil
}

// decodePayload returns the raw bytes of a JWT's payload segment, so claim
// and payload cannot disagree about what a token says.
func decodePayload(in []byte) ([]byte, error) {
	parts := strings.Split(strings.TrimSpace(string(in)), ".")
	if len(parts) < 3 {
		return nil, fmt.Errorf("input has %d segment(s), so it is not a JWT", len(parts))
	}
	if b, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
		return b, nil
	}
	// Some issuers pad; RawURLEncoding does not accept padding.
	b, err := base64.URLEncoding.DecodeString(parts[1] + strings.Repeat("=", (4-len(parts[1])%4)%4))
	if err != nil {
		return nil, fmt.Errorf("payload is not base64url: %w", err)
	}
	return b, nil
}

// count returns the length of a JSON array, or of the array under a single
// top-level key. Used where a suite asserts "how many sessions/users exist"
// and must tell an empty list from a failed request: an error here is not 0.
func count(in []byte) (string, error) {
	var arr []any
	if err := json.Unmarshal(in, &arr); err == nil {
		return strconv.Itoa(len(arr)), nil
	}
	var obj map[string]any
	if err := json.Unmarshal(in, &obj); err != nil {
		return "", fmt.Errorf("input is neither a JSON array nor an object: %w", err)
	}
	for _, v := range obj {
		if a, ok := v.([]any); ok {
			return strconv.Itoa(len(a)), nil
		}
	}
	return "", fmt.Errorf("object has no array field to count")
}

// pick finds one object in an array and returns a field of it: the shape of
// "the user whose email is X, give me its id".
//
// arrayKey is the field holding the array ("users"), or "." when the payload
// IS the array. match is key=value. Returns empty with no error when nothing
// matches, because absence is a legitimate answer a caller asserts on —
// distinct from an error, which means the payload could not be read at all.
func pick(in []byte, arrayKey, match, field string) (string, error) {
	k, want, ok := strings.Cut(match, "=")
	if !ok {
		return "", fmt.Errorf("match %q is not key=value", match)
	}

	var items []any
	if arrayKey == "." || arrayKey == "" {
		if err := json.Unmarshal(in, &items); err != nil {
			return "", fmt.Errorf("input is not a JSON array: %w", err)
		}
	} else {
		var obj map[string]any
		if err := json.Unmarshal(in, &obj); err != nil {
			return "", fmt.Errorf("input is not a JSON object: %w", err)
		}
		raw, present := obj[arrayKey]
		if !present {
			return "", fmt.Errorf("no field %q in the payload", arrayKey)
		}
		items, ok = raw.([]any)
		if !ok {
			return "", fmt.Errorf("field %q is %T, not an array", arrayKey, raw)
		}
	}

	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if fmt.Sprint(m[k]) != want {
			continue
		}
		v, present := m[field]
		if !present {
			return "", nil
		}
		return fmt.Sprint(v), nil
	}
	return "", nil
}

// urldecode reverses form encoding, for reading a value back out of a
// redirect's query string.
func urldecode(in []byte) (string, error) {
	s, err := url.QueryUnescape(strings.TrimSpace(string(in)))
	if err != nil {
		return "", fmt.Errorf("input is not form-encoded: %w", err)
	}
	return s, nil
}
