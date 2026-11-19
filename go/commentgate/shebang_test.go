package main

import "testing"

// A step script's shebang, as it appears in a diff (indented inside a YAML block
// scalar), must be exempt like any other functional directive.
func TestShebangIndented(t *testing.T) {
	for _, line := range []string{
		"            #!/bin/bash",
		"            #!/usr/bin/env bash",
		"            #!/busybox/sh",
		"#!/bin/sh",
	} {
		if !functionalRe.MatchString(line) {
			t.Errorf("%q is counted as prose", line)
		}
	}
}
