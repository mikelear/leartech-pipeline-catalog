// pincheck asserts that a task YAML's pinned image tags are REAL released tags
// of ghcr.io/mikelear/leartech-checkers, and at least the first release that
// carries the binaries the task expects. Written after provide-e2erun pinned a
// tag without e2erun — "cp: can't stat" on every end2end PR while every
// catalog gate stayed green.
//
// Usage: pincheck -task tasks/end2end/pullrequest.yaml
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
)

var imageRe = regexp.MustCompile(`image:\s*(\S+)`)

// firstWithE2erun is the first released checkers tag carrying e2erun (the
// release whose Dockerfile added the 11th binary). A pin older than this
// cannot provide it.
const firstWithE2erun = "0.11.35"

func main() {
	task := flag.String("task", "", "path to the task YAML")
	flag.Parse()
	if *task == "" {
		fmt.Fprintln(os.Stderr, "pincheck: -task is required")
		os.Exit(2)
	}
	b, err := os.ReadFile(*task)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pincheck:", err)
		os.Exit(2)
	}
	tags, err := releasedTags()
	if err != nil {
		fmt.Fprintln(os.Stderr, "pincheck: registry:", err)
		os.Exit(2)
	}
	exists := map[string]bool{}
	for _, t := range tags {
		exists[t] = true
	}
	fail := false
	seen := map[string]bool{}
	for _, m := range imageRe.FindAllStringSubmatch(string(b), -1) {
		ref := strings.Trim(m[1], `"`)
		if !strings.HasPrefix(ref, "ghcr.io/mikelear/leartech-checkers:") {
			continue
		}
		tag := strings.TrimPrefix(ref, "ghcr.io/mikelear/leartech-checkers:")
		if seen[tag] {
			continue
		}
		seen[tag] = true
		switch {
		case tag == "latest":
			fmt.Printf("FAIL: %s pins :latest — unpinnable, every run is a different image\n", *task)
			fail = true
		case !exists[tag]:
			fmt.Printf("FAIL: %s pins leartech-checkers:%s — no such released tag (tags: ...%v)\n", *task, tag, tags[len(tags)-3:])
			fail = true
		case semverLess(tag, firstWithE2erun):
			fmt.Printf("FAIL: %s pins leartech-checkers:%s — predates e2erun (first carrying it: %s)\n", *task, tag, firstWithE2erun)
			fail = true
		}
	}
	if fail {
		os.Exit(1)
	}
	fmt.Printf("==> pincheck: %d checkers tag(s) verified against the registry\n", len(seen))
}

// releasedTags lists the public image's tags from ghcr's registry API.
func releasedTags() ([]string, error) {
	resp, err := http.Get("https://ghcr.io/token?scope=repository:mikelear/leartech-checkers:pull&service=ghcr.io")
	if err != nil {
		return nil, err
	}
	var tok struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&tok)
	_ = resp.Body.Close()

	req, _ := http.NewRequest(http.MethodGet, "https://ghcr.io/v2/mikelear/leartech-checkers/tags/list?n=1000", nil)
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp2.Body.Close() }()
	var out struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Tags, nil
}

// semverLess compares dotted numeric tags.
func semverLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		var ai, bi int
		fmt.Sscanf(as[i], "%d", &ai)
		fmt.Sscanf(bs[i], "%d", &bi)
		if ai != bi {
			return ai < bi
		}
	}
	return len(as) < len(bs)
}
