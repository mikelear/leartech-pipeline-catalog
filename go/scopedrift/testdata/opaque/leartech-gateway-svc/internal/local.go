package gateway

const scopeChat = "leartechapi:gateway:chat"

func wire() { g.GET("", middleware.requireScope(scopeChat)) }
