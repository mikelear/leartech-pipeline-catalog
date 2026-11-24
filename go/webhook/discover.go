// discover.go builds the set of repos a hook belongs on, from the forge rather than a list.
//
// Two reasons this exists:
//
//	A hand-kept repo list is a snapshot. The estate has 121 SourceRepositories and grows every
//	week; a file listing them is wrong the first time someone creates a repo, and the symptom
//	is a repo with no CI that nobody notices until a PR sits with no checks.
//
//	-prune needs a SCOPE. "The repos that should have this hook" and "the repos that do" can
//	only be subtracted if the first is a query, not a list. An org + a filter is a query; a
//	list is not, because there is nothing to say what is missing from it.
package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Discover is the query half of a hook's target set.
type Discover struct {
	// Org or User: one of the two. GitHub and Gitea both expose both.
	// proven-by: TestDiscover_RequiresExactlyOneOfOrgAndUser
	Org  string `yaml:"org"`
	User string `yaml:"user"`
	// Include and Exclude are regular expressions on the repo NAME (not owner/name); an empty
	// Include takes every repo the forge lists.
	// proven-by: TestDiscoverKeep_IncludeAndExclude
	Include string `yaml:"include"`
	Exclude string `yaml:"exclude"`
	// Archived false (the default) skips archived repos: they take no pushes, so a hook on one
	// is dead weight that reads as coverage.
	Archived *bool `yaml:"archived"`
	// IncludePrivate defaults to TRUE. The estate's repos are mostly private, so defaulting
	// the other way would silently find nothing.
	IncludePrivate *bool `yaml:"include_private"`
	// IncludeForks defaults to false.
	IncludeForks *bool `yaml:"include_forks"`
}

// RepoInfo is what the forge reports about one repository, filtered on.
type RepoInfo struct {
	Repo     Repo
	Archived bool
	Private  bool
	Fork     bool
}

// owner is the single org-or-user this query names.
func (d Discover) owner() (string, error) {
	switch {
	case d.Org != "" && d.User != "":
		return "", fmt.Errorf("discover: set org or user, not both")
	case d.Org != "":
		return d.Org, nil
	case d.User != "":
		return d.User, nil
	default:
		return "", fmt.Errorf("discover: one of org or user is required")
	}
}

// compiled returns the two patterns, or an error naming the bad one.
func (d Discover) compiled() (include, exclude *regexp.Regexp, err error) {
	if d.Include != "" {
		if include, err = regexp.Compile(d.Include); err != nil {
			return nil, nil, fmt.Errorf("discover include %q: %w", d.Include, err)
		}
	}
	if d.Exclude != "" {
		if exclude, err = regexp.Compile(d.Exclude); err != nil {
			return nil, nil, fmt.Errorf("discover exclude %q: %w", d.Exclude, err)
		}
	}
	return include, exclude, nil
}

// keep is the whole filter, in one place, so it is testable without a forge.
//
// proven-by: TestDiscoverKeep_FiltersArchivedPrivateAndForks
func (d Discover) keep(r RepoInfo, include, exclude *regexp.Regexp) bool {
	if r.Archived && !boolOr(d.Archived, false) {
		return false
	}
	if r.Private && !boolOr(d.IncludePrivate, true) {
		return false
	}
	if r.Fork && !boolOr(d.IncludeForks, false) {
		return false
	}
	if include != nil && !include.MatchString(r.Repo.Name) {
		return false
	}
	if exclude != nil && exclude.MatchString(r.Repo.Name) {
		return false
	}
	return true
}

// boolOr gives an optional flag its default.
func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// listRepos is the forge call a discover block needs.
type listRepos func(owner string, isOrg bool) ([]RepoInfo, error)

// discoverRepos turns a query into the repos it matches, sorted so two runs agree.
// proven-by: TestDiscoverRepos_AppliesTheFilterAndSorts
func discoverRepos(d Discover, list listRepos) ([]Repo, error) {
	owner, err := d.owner()
	if err != nil {
		return nil, err
	}
	include, exclude, err := d.compiled()
	if err != nil {
		return nil, err
	}
	all, err := list(owner, d.Org != "")
	if err != nil {
		return nil, err
	}
	var out []Repo
	for _, r := range all {
		if d.keep(r, include, exclude) {
			out = append(out, r.Repo)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

// mergeRepos is the union of an explicit list and a discovered set, de-duplicated by
// owner/name, keeping the explicit spelling of a repo that appears in both.
// proven-by: TestMergeRepos_UnionWithoutDuplicates
func mergeRepos(explicit, discovered []Repo) []Repo {
	seen := make(map[string]bool, len(explicit)+len(discovered))
	out := make([]Repo, 0, len(explicit)+len(discovered))
	for _, list := range [][]Repo{explicit, discovered} {
		for _, r := range list {
			key := strings.ToLower(r.String())
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}
