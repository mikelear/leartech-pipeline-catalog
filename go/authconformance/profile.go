package main

// THE SERVICE PROFILE: what kind of auth participant this repo is, declared in
// a `.authprofile` file and CROSS-CHECKED against what the code actually does.
//
// WHY A TYPE AT ALL. There is no single "standard auth env set", and pretending
// there is caused real drift. An inbound-only resource server needs exactly
// LEARTECH_AUTH_ISSUER and LEARTECH_AUTH_AUDIENCE; giving it CLIENT_ID and
// CLIENT_SECRET makes it look like it holds an identity it never spends, which
// is how several services ended up carrying inert credentials that an operator
// could reasonably believe were load-bearing. A publisher needs the opposite
// set and no audience of its own. The correct configuration is a function of
// the TYPE, so the type has to be known before anything can be checked.
//
// WHY DECLARED *AND* DERIVED, NOT ONE OR THE OTHER.
//
// Derivation alone guesses. On 2026-09-12 a derived heuristic — "a UUID
// client_id means dynamically registered" — classified the platform's own
// oauth-frontend SPA client as a stranger holding internal audiences, and
// produced a security finding that was not real. A false finding is worse than
// none: it teaches people to ignore the check.
//
// Declaration alone drifts. A service declares inbound-only, then someone adds
// an outbound call, and the declaration is now a comment that lies.
//
// Declared plus a mechanical cross-check catches exactly the failure neither
// catches alone: "you declare inbound-only, but line 41 constructs
// NewServiceClient and line 88 spends a token".
//
// WHAT IT WILL AND WILL NOT FAIL ON. It fails on CONTRADICTIONS the evidence
// proves, never on absence of evidence. "I could not find a verifier" is not
// proof there is none — the constructor may be behind an interface, a helper,
// or generated code — so that is reported and not failed. "You declare
// outbound-only and here is your inbound verifier" is proof, and fails.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// serviceType is the declared kind of auth participant.
type serviceType string

const (
	// typeInbound validates tokens and mints none. An OAuth2 resource server.
	// Env: LEARTECH_AUTH_ISSUER + LEARTECH_AUTH_AUDIENCE. Nothing else.
	typeInbound serviceType = "inbound-resource-server"

	// typeDualRole validates inbound tokens AND mints its own for outbound
	// calls. The only type that legitimately holds client credentials while
	// also having an audience.
	typeDualRole serviceType = "dual-role"

	// typeOutbound mints tokens and exposes no authenticated API of its own —
	// controllers and event publishers. It has NO audience: nothing calls it.
	typeOutbound serviceType = "outbound-only"

	// typePublic is an inbound resource server facing EXTERNAL, dynamically
	// registered clients. Its audience is its own URL, because RFC 8707's
	// resource indicator is a URI and RFC 9728 publishes it at
	// /.well-known/oauth-protected-resource.
	typePublic serviceType = "public-resource-server"

	// typeIssuer is the auth service itself. It cannot be derived — the issuer
	// validates tokens it also mints — so this one is taken on declaration with
	// a mandatory reason.
	typeIssuer serviceType = "issuer"

	// typeNone does not participate in auth: batch jobs, static sites.
	typeNone serviceType = "none"
)

var knownTypes = []serviceType{
	typeInbound, typeDualRole, typeOutbound, typePublic, typeIssuer, typeNone,
}

func knownTypeList() string {
	out := make([]string, 0, len(knownTypes))
	for _, t := range knownTypes {
		out = append(out, string(t))
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

type profile struct {
	declared serviceType
	reason   string
	present  bool
}

// evidence is what the repo's source and chart actually prove.
type evidence struct {
	verifierAt      []string
	serviceClientAt []string
	outboundAt      []string
	protectedResAt  []string
	chartEnv        map[string]string
}

func (e evidence) hasVerifier() bool      { return len(e.verifierAt) > 0 }
func (e evidence) hasServiceClient() bool { return len(e.serviceClientAt) > 0 }
func (e evidence) hasOutbound() bool      { return len(e.outboundAt) > 0 }
func (e evidence) hasProtectedRes() bool  { return len(e.protectedResAt) > 0 }

// derive infers the type from evidence, or "" when the evidence does not
// determine one. Ambiguity returns "" deliberately rather than a best guess:
// a wrong derived type turns into a wrong failure.
func (e evidence) derive() serviceType {
	switch {
	case e.hasProtectedRes() && e.hasVerifier():
		return typePublic
	case e.hasVerifier() && e.hasOutbound():
		return typeDualRole
	case e.hasVerifier():
		return typeInbound
	case e.hasOutbound() || e.hasServiceClient():
		return typeOutbound
	case !e.hasVerifier() && !e.hasServiceClient() && !e.hasOutbound():
		return typeNone
	}
	return ""
}

func loadProfile(root string, r *report, ev evidence) profile {
	var p profile
	b, err := os.ReadFile(filepath.Join(root, ".authprofile"))
	if err != nil {
		// A missing profile is a failure, but a USEFUL one: it names the type
		// the evidence points at, so adopting it is a one-line paste rather
		// than a research task.
		suggested := ev.derive()
		hint := "could not be determined from the source"
		if suggested != "" {
			hint = fmt.Sprintf("looks like %q based on %s", suggested, ev.summary())
		}
		r.fail("profile-declared", ".authprofile",
			"no .authprofile. Declare what kind of auth participant this service is — the correct env set, chart shape and test properties all depend on it, and there is no single standard set. This service %s.\n\n    Write .authprofile:\n\n      type: %s\n\n    Valid types: %s",
			hint, firstNonEmpty(string(suggested), string(typeInbound)), knownTypeList())
		return p
	}

	p.present = true
	var last string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, hasColon := strings.Cut(line, ":")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch {
		case hasColon && k == "type":
			p.declared = serviceType(v)
			last = "type"
		case hasColon && k == "reason":
			p.reason = v
			last = "reason"
		case last == "reason":
			// Reasons wrap. Without this the middle of a paragraph is dropped
			// and the printed justification reads as nonsense, which is worse
			// than none because it looks like one.
			p.reason += " " + line
		}
	}

	if p.declared == "" {
		r.fail("profile-declared", ".authprofile",
			"has no `type:`. Valid types: %s", knownTypeList())
		return p
	}

	known := false
	for _, t := range knownTypes {
		if p.declared == t {
			known = true
		}
	}
	if !known {
		r.fail("profile-declared", ".authprofile",
			"declares unknown type %q. Valid types: %s", p.declared, knownTypeList())
		return p
	}

	// The issuer cannot be cross-checked — it validates tokens it also mints —
	// so it is the one type taken on trust, and trust needs an argument.
	if p.declared == typeIssuer && strings.TrimSpace(p.reason) == "" {
		r.fail("profile-declared", ".authprofile",
			"declares type: issuer with no `reason:`. That type is exempt from the derived cross-check because an issuer validates the tokens it mints, so it is the one declaration nothing can contradict. State why this service is the issuer.")
	}

	fmt.Printf("    profile: %s\n", p.declared)
	return p
}

// summary describes what was actually found, for messages. Without it a
// contradiction reads as an assertion; with it, the reader can check the tool's
// reasoning and disagree.
func (e evidence) summary() string {
	var parts []string
	if e.hasVerifier() {
		parts = append(parts, "auth.NewVerifier at "+e.verifierAt[0])
	}
	if e.hasServiceClient() {
		parts = append(parts, "auth.NewServiceClient at "+e.serviceClientAt[0])
	}
	if e.hasOutbound() {
		parts = append(parts, "an outbound token spend at "+e.outboundAt[0])
	}
	if e.hasProtectedRes() {
		parts = append(parts, "RFC 9728 protected-resource metadata at "+e.protectedResAt[0])
	}
	if len(parts) == 0 {
		return "no auth constructors or outbound token spends in non-test source"
	}
	return strings.Join(parts, ", ")
}

// checkProfile fails only where the evidence CONTRADICTS the declaration.
func checkProfile(p profile, ev evidence, r *report) {
	if !p.present || p.declared == "" {
		return // already reported by loadProfile
	}
	if p.declared == typeIssuer {
		return // not cross-checkable by construction; reason enforced above
	}

	contradiction := func(why string) {
		r.fail("profile-matches-code", ".authprofile",
			"declares type: %s, but %s.\n\n    Evidence: %s\n\n    Either the declaration is stale or the code drifted. Both are worth a diff.",
			p.declared, why, ev.summary())
	}

	switch p.declared {
	case typeInbound:
		// Proven outbound is a real contradiction: an inbound-only service
		// holds no client identity, and this is the shape that put inert
		// credentials into charts.
		if ev.hasOutbound() {
			contradiction("it spends an outbound token, which makes it dual-role")
		}
		// Nothing validating at all, while auth is clearly in use, means the
		// declaration describes an intent the code does not implement.
		if !ev.hasVerifier() && (ev.hasServiceClient() || ev.hasOutbound()) {
			contradiction("no inbound verifier was found, so nothing validates incoming tokens")
		}

	case typeDualRole:
		if !ev.hasOutbound() {
			contradiction("no outbound token spend was found. ServiceClient's validateConfig demands ClientID and ClientSecret it would never use; their absence took plan-api off the air on 2026-08-13. Forwarding an inbound bearer to a peer is NOT an outbound leg")
		}

	case typeOutbound:
		if ev.hasVerifier() {
			contradiction("it constructs an inbound verifier, so it does expose an authenticated API and needs an audience of its own")
		}

	case typePublic:
		if !ev.hasProtectedRes() {
			contradiction("no RFC 9728 protected-resource metadata was found. A public resource server publishes /.well-known/oauth-protected-resource so a dynamically-registered client can discover the issuer and the resource identifier to request")
		}

	case typeNone:
		if ev.hasVerifier() || ev.hasServiceClient() || ev.hasOutbound() {
			contradiction("it uses go-common/pkg/auth, so it does participate in auth")
		}
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
