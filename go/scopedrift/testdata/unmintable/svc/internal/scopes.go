package config

const ScopeClientsRead auth.Scope = "leartechapi:ba:clients_read"
const ScopeSecret auth.Scope = "leartechapi:ba:secret_read"

func wire() {
	verifier.RequireScope(config.ScopeClientsRead)
	verifier.RequireScope(ScopeSecret)
}
