package config

const ScopeClientsRead auth.Scope = "leartechapi:ba:clients_read"

func wire() { verifier.RequireScope(config.ScopeClientsRead) }
