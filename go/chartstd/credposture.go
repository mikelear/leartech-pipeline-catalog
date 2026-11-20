package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// S13 — a chart has ONE way of proving it may write to the bucket, and it is the deploy's
// choice, never a silent default.
//
// WHY. artifact-api#46 made keyless V4 signing possible (`gcs.signerEmail` → iam.signBlob)
// next to the historical key-file path (`gcs.secretName` → a mounted key). Both are
// legitimate; a chart that accepts BOTH AT ONCE is not, and it is the shape that hides:
//
//	gcs:
//	  bucket: artifacts
//	  secretName: gcs-key          # the key wins
//	  signerEmail: writer@proj.iam.gserviceaccount.com
//
// The SDK signs off the key whenever one is mounted, so `signerEmail` is inert. The chart
// therefore *says* keyless is supported, every deployment silently uses the key, and the
// keyless path is never exercised until the day someone removes the mount — in production.
// That is the precise failure the artifact-api work removed, and a chart default is how it
// comes back.
//
//	no signing posture at all, with a bucket
//	  bucket: artifacts
//	  (neither secretName nor signerEmail)
//
// fails the other way: every deployment crash-loops at boot, because the store refuses to
// serve a URL it cannot sign. The fail-fast is correct; the chart shipping a configuration
// that can ONLY crash is not.
//
// NO ESTATE CHART FIRES THIS TODAY — artifact-api's defaults are all empty and its overlay
// supplies exactly one posture. The fixtures pin the rule and the shapes, so flipping the
// chart's default (step 4 of artifact-api#46) has a guard in place BEFORE it happens, which
// is the order that issue asked for.
//
// proven-by: TestCredentialPostureIsSingular
func credentialFindings(chartDir string) []Finding {
	path := filepath.Join(chartDir, "values.yaml")
	b, err := os.ReadFile(path) // #nosec G304 -- the operator names the chart
	if err != nil {
		// S10 already refuses to pass a chart whose values cannot be read; two rules
		// reporting one unreadable file is noise.
		return nil
	}

	var vals map[string]any
	if err := yaml.Unmarshal(b, &vals); err != nil {
		return nil
	}

	var out []Finding
	var walk func(prefix string, m map[string]any)
	walk = func(prefix string, m map[string]any) {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		// The posture keys this block carries, by the leaf name that names each one.
		var bucket, secretName, signerEmail string
		// Whether the stanza DECLARES a signing mechanism at all. A `gcs.bucket` on a
		// service that only reads objects is not this rule's business; a stanza that has a
		// `secretName` or `signerEmail` key is one that signs URLs, and then its posture is
		// judgeable. Without this, S13 fired on three SOAR charts whose credential key is
		// spelled `credentialSecretRef` — measured on the estate sweep, and fixed here
		// rather than by teaching the rule every spelling it might meet.
		var declaresPosture bool

		for _, k := range keys {
			path2 := k
			if prefix != "" {
				path2 = prefix + "." + k
			}
			if child, ok := m[k].(map[string]any); ok {
				walk(path2, child)
				continue
			}
			v, _ := m[k].(string)
			switch strings.ToLower(k) {
			case "bucket":
				bucket = strings.TrimSpace(v)
			case "secretname":
				secretName = strings.TrimSpace(v)
				declaresPosture = true
			case "signeremail":
				signerEmail = strings.TrimSpace(v)
				declaresPosture = true
			}
		}

		// Only judge a stanza that DECLARES a signing posture. Every chart in the estate
		// has `secretName:` keys (a bot token, a git credential, a registry pull secret),
		// and every service with a bucket is not necessarily one that signs URLs.
		if !declaresPosture {
			return
		}

		where := prefix
		if where == "" {
			where = "(root)"
		}

		switch {
		case secretName != "" && signerEmail != "":
			out = append(out, Finding{"S13", path, fmt.Sprintf(
				"%s sets BOTH secretName (%q) and signerEmail (%q). The key wins silently: the "+
					"SDK signs off the mounted file whenever there is one, so the keyless path is "+
					"dead code in every deployment that inherits this default — and removing the "+
					"mount later is when it first runs. Leave one empty and let the deploy choose.",
				where, secretName, signerEmail)})
		case bucket != "" && secretName == "" && signerEmail == "":
			out = append(out, Finding{"S13", path, fmt.Sprintf(
				"%s names a bucket (%q) and NO signing posture, so every deployment of this "+
					"chart crash-loops at boot — the store refuses to serve a URL it cannot sign, "+
					"and that is correct. A chart may not ship a configuration that can only fail; "+
					"leave bucket empty too, and let the deploy supply both.",
				where, bucket)})
		}
	}
	walk("", vals)
	return out
}
