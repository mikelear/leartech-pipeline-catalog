package main

import (
	"strings"
	"testing"
)

// EVERY CASE HERE IS A BUG THAT HAPPENED.
//
// The shell version of this anchor needed four corrections in one day, each a
// pure input-to-decision mistake. A pipeline re-run diagnoses one preview at a
// time; a table settles it.
func TestImageTag(t *testing.T) {
	for _, tc := range []struct {
		name, ref, want string
	}{
		// Correction 3. jx pins images as name:tag@sha256:digest. Taking
		// everything after the last colon yields the digest hex, so every
		// digest-pinned preview failed the anchor while serving the right
		// build, reporting "no running image carries VERSION=X" about an image
		// tagged exactly X.
		{"digest-pinned", "reg/app:0.0.0-PR-7-3@sha256:abc123", "0.0.0-PR-7-3"},

		{"plain tag", "reg/app:0.0.0-PR-7-3", "0.0.0-PR-7-3"},
		{"no tag", "reg/app", ""},

		// A registry port is a colon that is not a tag. Without the
		// slash-versus-colon check, "5000/app" comes back as the tag.
		{"registry port, no tag", "registry:5000/app", ""},
		{"registry port with tag", "registry:5000/app:1.2.3", "1.2.3"},

		{"digest only, no tag", "reg/app@sha256:abc123", ""},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := imageTag(tc.ref); got != tc.want {
				t.Errorf("imageTag(%q) = %q, want %q", tc.ref, got, tc.want)
			}
		})
	}
}

// Correction 2, and the one that certified a wrong build green.
//
// A substring comparison passes 0.0.0-PR-7-3 against a pod running
// 0.0.0-PR-7-30 — iteration 3 accepted as iteration 30. Iteration counts reach
// double digits on any PR that gets retested, so this is reachable rather than
// theoretical.
func TestMatch_IterationThreeIsNotIterationThirty(t *testing.T) {
	var out strings.Builder
	rc := cmdMatch(strings.NewReader("reg/app:0.0.0-PR-7-30\n"), &out, "0.0.0-PR-7-3")
	if rc == 0 {
		t.Fatalf("iteration 3 matched a pod running iteration 30 (%q). That certifies "+
			"the wrong build with a green tick.", strings.TrimSpace(out.String()))
	}
	if rc != 1 {
		t.Errorf("exit = %d, want 1 (no match)", rc)
	}
}

// The control. Without it, the test above passes against a matcher that never
// matches anything.
func TestMatch_FindsTheRightBuildAmongOthers(t *testing.T) {
	var out strings.Builder
	in := strings.NewReader(strings.Join([]string{
		"reg/sidecar:1.0.0",
		"reg/app:0.0.0-PR-7-30@sha256:deadbeef",
		"reg/app:0.0.0-PR-7-3@sha256:abc123",
	}, "\n"))
	if rc := cmdMatch(in, &out, "0.0.0-PR-7-3"); rc != 0 {
		t.Fatalf("exit = %d, want 0; the matching image was in the list", rc)
	}
	if got := strings.TrimSpace(out.String()); got != "reg/app:0.0.0-PR-7-3@sha256:abc123" {
		t.Errorf("printed %q, want the matching ref so a reader can see WHICH image matched", got)
	}
}

// A PROBE THAT INSPECTED NOTHING IS NOT A PROBE THAT FOUND NOTHING.
//
// Exit 2, distinct from exit 1. Falling through to "no match" would blame the
// version for what is really an unreadable cluster or missing RBAC, and send
// whoever reads it hunting the wrong thing.
func TestMatch_NoImagesIsNotAMismatch(t *testing.T) {
	var out strings.Builder
	rc := cmdMatch(strings.NewReader("   \n\n"), &out, "0.0.0-PR-7-3")
	if rc != 2 {
		t.Errorf("exit = %d, want 2 (nothing readable), distinct from 1 (mismatch)", rc)
	}
}

// An empty -expected would accept any build, which is worse than no anchor at
// all because it reports success.
//
// The input is an UNTAGGED ref on purpose. An earlier version of this test used
// a tagged one, where an empty expected never matches anyway — so it passed
// with the guard removed and proved nothing. Mutation testing caught that; the
// hazard is specifically imageTag("reg/app") == "" equalling an empty want and
// reporting a match.
func TestMatch_RefusesAnEmptyExpectedVersion(t *testing.T) {
	var out strings.Builder
	rc := cmdMatch(strings.NewReader("reg/app\n"), &out, "")
	if rc == 0 {
		t.Fatalf("an empty -expected matched an untagged image (%q), so the anchor "+
			"would certify any build as the right one", strings.TrimSpace(out.String()))
	}
	if rc != 64 {
		t.Errorf("exit = %d, want 64 (usage error); a mismatch exit would read as "+
			"'wrong build' when the real fault is a caller that passed nothing", rc)
	}
}

// Correction 1 and 4 live here: the caller must be able to tell "the gate did
// not tell me" from "the gate is broken", because treating them alike is how an
// unreadable gate came to kill the whole step under `set -eo pipefail`.
func TestExpected(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		rc               int
	}{
		{"present", `{"expected_version":"0.0.0-PR-7-3","other":1}`, "0.0.0-PR-7-3", 0},

		// The shell used a sed regex over one line. A reformatted body — the
		// key and value on separate lines — is valid JSON that the regex
		// cannot read, and it would report "cannot anchor" about a gate that
		// answered perfectly.
		{"reformatted across lines", "{\n  \"expected_version\"\n  :\n  \"0.0.0-PR-7-3\"\n}", "0.0.0-PR-7-3", 0},

		{"absent", `{"other":1}`, "", 3},
		{"empty string value", `{"expected_version":""}`, "", 3},
		{"not json", `expected_version=0.0.0`, "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			rc := cmdExpected(strings.NewReader(tc.body), &out)
			if rc != tc.rc {
				t.Errorf("exit = %d, want %d", rc, tc.rc)
			}
			if got := strings.TrimSpace(out.String()); got != tc.want {
				t.Errorf("printed %q, want %q", got, tc.want)
			}
		})
	}
}

// The failure message must name the fix, and it must show what it examined.
// "no running image carries version X" without the list sends the reader to
// kubectl to find out what this already knew.
func TestMatch_FailureShowsWhatItExamined(t *testing.T) {
	var out strings.Builder
	// stderr is not captured here; the assertion is that the matched-ref path
	// stays silent on stdout so a caller can consume stdout as the answer.
	if rc := cmdMatch(strings.NewReader("reg/app:9.9.9\n"), &out, "1.1.1"); rc != 1 {
		t.Fatalf("exit = %d, want 1", rc)
	}
	if out.String() != "" {
		t.Errorf("stdout = %q on a mismatch; it must stay empty so callers can treat "+
			"stdout as the matched reference", out.String())
	}
}
