package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The forms the estate actually has, copied from real files rather than
// invented. A fixture I made up would only prove the AST walk matches what I
// imagined a mount site looks like.
const (
	// leartech-maestro-service/internal/eventprocessor/handler.go before #17
	maestroBefore = `package eventprocessor
func NewHandler(rg *gin.RouterGroup, em domain.EventProcessorUseCase, au auth.ServiceAuthClient) {
	handler := Handler{EventMaestroUseCase: em, AuthUseCase: au}
	rg.POST("/announce_event", au.Middleware(nil), handler.AnnounceEvent)
	rg.POST("/consume_event", au.Middleware(nil), handler.ConsumeEvent)
}
`
	// the same file after #17
	maestroAfter = `package eventprocessor
func NewHandler(rg *gin.RouterGroup, em domain.EventProcessorUseCase, au auth.ServiceAuthClient) {
	rg.POST("/announce_event", au.Middleware(auth.Permissions{auth.PermAdmin}), handler.AnnounceEvent)
	rg.POST("/consume_event", au.Middleware(auth.Permissions{auth.PermAdmin}), handler.ConsumeEvent)
}
`
	// leartech-plan-conformance-consumer/internal/middleware/auth.go — the
	// template-derived shape, present identically in six repos.
	consumerShape = `package middleware
func BearerAuth(cfg auth.Config) gin.HandlerFunc {
	client, err := auth.NewServiceClient(context.Background(), cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("auth: failed to initialise service client")
	}
	return client.Middleware(nil)
}
`
	// leartech-ai-gateway/internal/server/server.go — the package-level
	// Verifier form, where the permissions are the SECOND argument.
	verifierForm = `package server
func mount(r *gin.Engine, verifier *auth.Verifier) {
	r.Use(auth.Middleware(verifier, nil))
}
`
	// leartech-auth-service/internal/handlers/auth.go — the same form, gated.
	verifierFormGated = `package handlers
func mount(api *gin.RouterGroup, h *Handler) {
	protected := api.Group("", auth.Middleware(h.verifier, auth.Permissions{auth.PermUser}))
	_ = protected
}
`
	// webcoder-service — an empty composite literal grants exactly as much as
	// nil, and reads as though it grants something.
	emptyComposite = `package middleware
func mount(r *gin.Engine, client auth.ServiceAuthClient) {
	r.Use(client.Middleware(auth.Permissions{}))
}
`
)

func TestTheShapesThisEstateActuallyHas(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"maestro before #17 — eight nil gates in one repo", maestroBefore, 1},
		{"maestro after #17", maestroAfter, 0},
		{"the template-derived BearerAuth in six repos", consumerShape, 1},
		{"package-level Verifier form with nil perms", verifierForm, 1},
		{"package-level Verifier form, gated", verifierFormGated, 0},
		{"empty composite literal grants nothing", emptyComposite, 1},
	}
	bin := buildOnce(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "x.go", tc.src)
			out, code := run(t, bin, "-dir", dir)
			if code != tc.want {
				t.Fatalf("exit %d, want %d\n%s", code, tc.want, out)
			}
		})
	}
}

// A repo with no middleware is not a repo with a hole in it. Exiting 1 here
// would fail every library and CLI in the estate.
func TestNoMiddlewareIsNotAFailure(t *testing.T) {
	bin := buildOnce(t)
	dir := t.TempDir()
	write(t, dir, "x.go", "package x\nfunc F() int { return 1 }\n")
	out, code := run(t, bin, "-dir", dir)
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "nothing to check") {
		t.Errorf("a repo with no middleware produced no explanation:\n%s", out)
	}
}

// Tests declare Middleware(nil) constantly — asserting a mock, building a
// harness. Gating on them would make the checker unusable.
func TestTestFilesAndMocksAreNotMountSites(t *testing.T) {
	bin := buildOnce(t)
	dir := t.TempDir()
	write(t, dir, "h_test.go", "package x\nfunc T() { mockAuth.EXPECT().Middleware(nil).Return(nil) }\n")
	write(t, dir, "client_mock.go", "package x\nfunc (m *Mock) Use() { m.Middleware(nil) }\n")
	if out, code := run(t, bin, "-dir", dir); code != 0 {
		t.Fatalf("exit %d, want 0 — a _test.go or _mock.go file was read as a mount site\n%s", code, out)
	}
}

// Declaring the method is not mounting a route.
func TestTheMethodDeclarationIsNotACallSite(t *testing.T) {
	bin := buildOnce(t)
	dir := t.TempDir()
	write(t, dir, "iface.go", `package x
type ServiceAuthClient interface {
	Middleware(requiredPerms Permissions) gin.HandlerFunc
}
`)
	if out, code := run(t, bin, "-dir", dir); code != 0 {
		t.Fatalf("exit %d, want 0 — an interface declaration was counted\n%s", code, out)
	}
}

// Unparseable Go is not a pass. A repo that stops compiling should not go quiet.
func TestBrokenGoIsNotAPass(t *testing.T) {
	bin := buildOnce(t)
	dir := t.TempDir()
	write(t, dir, "broken.go", "package x\nfunc (((\n")
	if _, code := run(t, bin, "-dir", dir); code != 2 {
		t.Fatalf("exit %d, want 2 for unparseable Go", code)
	}
}

func TestMissingDirIsACallerError(t *testing.T) {
	bin := buildOnce(t)
	if _, code := run(t, bin, "-dir", filepath.Join(t.TempDir(), "nope")); code != 64 {
		t.Fatalf("want 64 for a directory that does not exist")
	}
}

func buildOnce(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "authgate")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, bin string, args ...string) (string, int) {
	t.Helper()
	out, err := exec.Command(bin, args...).CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run: %v", err)
	}
	return string(out), code
}

// A WRAPPER HIDES THE GATE, and the estate's own template is the proof.
//
// leartech-go-service-template does not call Middleware(nil) anywhere. It
// calls middleware.BearerAuth(cfg.Auth, nil), and BearerAuth passes that
// straight to auth.Middleware. The first version of this checker reported the
// template CLEAN — the one repo that seeds the pattern for every new Go
// service, and the one six repos inherited their nil gate from.
func TestAWrapperTakingPermissionsIsFollowed(t *testing.T) {
	// Copied from leartech-go-service-template: internal/middleware/auth.go
	// declares the wrapper, cmd/server/router.go mounts with nil.
	const wrapperDecl = `package middleware
func BearerAuth(cfg auth.VerifierConfig, perms auth.Permissions) (gin.HandlerFunc, error) {
	verifier, err := auth.NewVerifier(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("bearer middleware: %w", err)
	}
	return auth.Middleware(verifier, perms), nil
}
`
	const mountNil = `package main
func newRouter(cfg Config) (*gin.Engine, error) {
	authed := router.Group("/api/v1")
	bearer, err := middleware.BearerAuth(cfg.Auth, nil)
	_ = authed
	return nil, err
}
`
	const mountGated = `package main
func newRouter(cfg Config) (*gin.Engine, error) {
	bearer, err := middleware.BearerAuth(cfg.Auth, auth.Permissions{auth.PermUser})
	return nil, err
}
`
	bin := buildOnce(t)

	t.Run("wrapper called with nil is caught", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "mw.go", wrapperDecl)
		write(t, dir, "router.go", mountNil)
		out, code := run(t, bin, "-dir", dir)
		if code != 1 {
			t.Fatalf("exit %d, want 1 — the wrapper hid the nil gate\n%s", code, out)
		}
		if !strings.Contains(out, "wrapper around Middleware") {
			t.Errorf("the finding does not say it came through a wrapper:\n%s", out)
		}
	})

	t.Run("wrapper called with permissions is clean", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "mw.go", wrapperDecl)
		write(t, dir, "router.go", mountGated)
		if out, code := run(t, bin, "-dir", dir); code != 0 {
			t.Fatalf("exit %d, want 0\n%s", code, out)
		}
	})

	t.Run("the wrapper declaration alone is not a mount", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "mw.go", wrapperDecl)
		if out, code := run(t, bin, "-dir", dir); code != 0 {
			t.Fatalf("exit %d, want 0 — declaring a wrapper is not mounting a route\n%s", code, out)
		}
	})
}
