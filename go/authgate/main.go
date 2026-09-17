// authgate fails a PR that mounts an authenticated route without authorising it.
//
// THE BUG THIS EXISTS FOR. go-common's middleware returns true before it reads
// the claims when the required permission set is empty:
//
//	func isTokenAllowedAccess(requiredPerms Permissions, claims *TokenClaims) bool {
//		if len(requiredPerms) == 0 { return true }
//		...
//	}
//
// So `Middleware(nil)` is not a lenient gate. It is the absence of one: the
// route authenticates every caller and authorises all of them, and the 403
// branch is unreachable. No behavioural test catches it, because there is no way to
// write a test for a refusal the code has no branch to make.
//
// Measured 2026-09-17: leartech-maestro-service mounted all EIGHT of its
// private routes this way. Any token the estate could mint — a browser's user
// token as readily as a service's — could announce an event to the bus,
// consume one, or reprocess another producer's. Every check on every PR that
// touched those files was green for as long as the code existed.
//
// WHY A CHECKER AND NOT A TEST. An AST test in one repo protects one repo, and
// this is a shape any Go service using go-common can grow. maestro carries the
// original as TestNoPrivateRouteIsMountedWithEmptyPermissions; this is that
// question asked of every repo, by default, without anyone adding a file.
//
// Both call forms in the estate are checked:
//
//	au.Middleware(perms)              ServiceClient method
//	auth.Middleware(verifier, perms)  package-level Verifier form
//
// Exit codes
//
//	0   every mounted route asks for something, or there is nothing to check
//	1   a route is mounted with no permissions
//	2   a file named as Go could not be read or parsed
//	64  the caller passed something unusable
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type finding struct {
	file string
	line int
	call string
}

func main() {
	dir := flag.String("dir", ".", "directory to scan for Go sources")
	flag.Parse()

	if strings.TrimSpace(*dir) == "" {
		fmt.Fprintln(os.Stderr, "FAIL: -dir is empty; nothing to check.")
		os.Exit(64)
	}
	if fi, err := os.Stat(*dir); err != nil || !fi.IsDir() {
		fmt.Fprintf(os.Stderr, "FAIL: %s is not a directory.\n", *dir)
		os.Exit(64)
	}

	var found []finding
	examined := 0
	fset := token.NewFileSet()

	walkErr := filepath.WalkDir(*dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Vendored and generated trees are not ours to gate.
			switch d.Name() {
			case "vendor", ".git", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		// Generated mocks re-declare Middleware and are not mount sites.
		if strings.Contains(filepath.Base(p), "_mock") || strings.Contains(filepath.Base(p), "mock_") {
			return nil
		}
		f, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			fmt.Fprintf(os.Stderr, "FAIL: unable to parse %s: %v\n", p, perr)
			os.Exit(2)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Middleware" {
				return true
			}
			// Declaring the method is not mounting a route.
			if len(call.Args) == 0 {
				return true
			}
			examined++
			// One arg  -> ServiceClient.Middleware(perms)
			// Two args -> auth.Middleware(verifier, perms)
			arg := call.Args[len(call.Args)-1]
			if !isEmptyPerms(arg) {
				return true
			}
			pos := fset.Position(call.Pos())
			found = append(found, finding{
				file: pos.Filename, line: pos.Line, call: render(sel, call),
			})
			return true
		})
		return nil
	})
	if walkErr != nil {
		fmt.Fprintf(os.Stderr, "FAIL: unable to walk %s: %v\n", *dir, walkErr)
		os.Exit(2)
	}

	// A repo with no middleware is not a repo with a hole in it. Say what was
	// examined either way: a checker that prints nothing is indistinguishable
	// from one that did not run.
	if examined == 0 {
		fmt.Printf("==> authgate: no Middleware call sites under %s; nothing to check.\n", *dir)
		os.Exit(0)
	}

	if len(found) == 0 {
		fmt.Printf("==> authgate: %d Middleware call site(s), all asking for permissions.\n", examined)
		os.Exit(0)
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].file != found[j].file {
			return found[i].file < found[j].file
		}
		return found[i].line < found[j].line
	})
	var b strings.Builder
	for _, f := range found {
		fmt.Fprintf(&b, "  %s:%d  %s\n", f.file, f.line, f.call)
	}
	fmt.Fprintf(os.Stderr,
		"FAIL: %d of %d route(s) mount with NO required permissions:\n\n%s\n"+
			"An empty permission set is not a weak gate, it is the absence of one.\n"+
			"go-common returns true before it reads the claims:\n\n"+
			"    if len(requiredPerms) == 0 { return true }\n\n"+
			"so the route authenticates every caller and authorises all of them, and the\n"+
			"403 branch is unreachable. leartech-maestro-service shipped eight of these;\n"+
			"any token the estate could mint could write to the event bus.\n\n"+
			"Pass what the route needs — auth.Permissions{auth.PermAdmin} for writes,\n"+
			"auth.Permissions{auth.PermUser} for reads. Internal callers are unaffected:\n"+
			"isTokenAllowedAccess short-circuits on the internal_services scope, which\n"+
			"every s2s client already holds.\n",
		len(found), examined, b.String())
	os.Exit(1)
}

// isEmptyPerms reports whether an argument grants nothing: a bare nil, or a
// composite literal with no elements such as auth.Permissions{}.
func isEmptyPerms(arg ast.Expr) bool {
	if id, ok := arg.(*ast.Ident); ok && id.Name == "nil" {
		return true
	}
	if cl, ok := arg.(*ast.CompositeLit); ok && len(cl.Elts) == 0 {
		return true
	}
	return false
}

func render(sel *ast.SelectorExpr, call *ast.CallExpr) string {
	recv := "?"
	if id, ok := sel.X.(*ast.Ident); ok {
		recv = id.Name
	}
	args := make([]string, 0, len(call.Args))
	for _, a := range call.Args {
		switch v := a.(type) {
		case *ast.Ident:
			args = append(args, v.Name)
		case *ast.CompositeLit:
			args = append(args, "{}")
		default:
			args = append(args, "…")
		}
	}
	return fmt.Sprintf("%s.Middleware(%s)", recv, strings.Join(args, ", "))
}
