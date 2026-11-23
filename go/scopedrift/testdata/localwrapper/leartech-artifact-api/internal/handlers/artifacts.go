package handlers

// The shape leartech-artifact-api actually uses: a repo-local RequireScope wrapper and
// constants with NO type at all. Both are enforcement; nothing here names go-common.
//
//	const ScopeArtifactRead = "leartechapi:artifact:read"
//	group.GET("", middleware.RequireScope(ScopeArtifactRead), h.List)
const ScopeArtifactRead = "leartechapi:artifact:read"
const ScopeArtifactWrite = "leartechapi:artifact:write"

type router interface {
	GET(string, int, handler)
	POST(string, int, handler)
}

type handler int

func wire(group router, h handler) {
	group.GET("", middleware.RequireScope(ScopeArtifactRead), h)
	group.GET("/:id", middleware.RequireScope(ScopeArtifactRead), h)
	group.POST("", middleware.RequireScope(ScopeArtifactWrite), h)
}
