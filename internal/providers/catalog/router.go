package catalog

import (
	"github.com/xianxu/charon/internal/proxy"
)

// Register adds each catalog entry's hostname patterns to the
// proxy's HostToProvider map as exact-match rules. Compiled-
// provider entries (declared statically in routing.go) take
// precedence: Register skips any hostname already registered.
//
// Called once at startup from the serve command. Idempotent for
// the catalog's own entries (re-registering replaces them).
func Register(c *Catalog) {
	for _, e := range c.Entries {
		rp := EntryToProvider(e)
		for _, host := range e.HostnamePatterns {
			if _, exists := proxy.HostToProvider[host]; exists {
				// Compiled provider already owns this host.
				continue
			}
			proxy.HostToProvider[host] = rp
		}
	}
}

// EntryToProvider builds a proxy.Provider from a catalog Entry.
// Exposed for tests; production code should use Register.
func EntryToProvider(e Entry) *proxy.Provider {
	p := &proxy.Provider{
		Name:          e.ID,
		VaultProvider: e.ID,
		ExtraHeaders:  e.Auth.ExtraHeaders,
		HasScopes:     false, // catalog providers have no scope semantics
	}
	switch e.Auth.Style {
	case "bearer":
		p.Auth = proxy.AuthBearer
		p.HeaderPrefix = e.Auth.Prefix // empty → InjectAuth defaults to "Bearer "
	case "header":
		p.Auth = proxy.AuthHeader
		p.HeaderName = e.Auth.Header
		p.HeaderPrefix = e.Auth.Prefix
	case "query":
		p.Auth = proxy.AuthQuery
		p.HeaderName = e.Auth.Param // empty → InjectAuth defaults to "key"
	}
	return p
}
