// Package proxy implements the Charon HTTPS forward proxy.
package proxy

import (
	"fmt"
	"strings"
)

// AuthMethod defines how credentials are injected into requests.
type AuthMethod string

const (
	AuthBearer AuthMethod = "bearer" // Authorization: Bearer <token>
	// Future: AuthBasic, AuthHeader, AuthQuery, AuthAWSSigV4
)

// Provider describes a credential provider and how to inject auth.
//
// HasScopes controls whether X-Charon-Scope is honored for routes
// to this provider. OAuth providers (Google) have scope semantics
// and consume the header. Admin-key providers (OpenAI) and catalog
// providers have no scope concept — the header is silently ignored
// on their routes per the agent-protocol contract. Charon strips
// the header from outbound requests in either case.
type Provider struct {
	Name      string
	Auth      AuthMethod
	HasScopes bool
}

// InjectAuth adds the credential to the request headers.
func (p *Provider) InjectAuth(setHeader func(key, value string), token string) error {
	switch p.Auth {
	case AuthBearer, "": // default to bearer
		setHeader("Authorization", "Bearer "+token)
		return nil
	default:
		return fmt.Errorf("unsupported auth method: %s", p.Auth)
	}
}

// HostToProvider maps exact API hosts to credential providers.
//
// OpenAI's data plane is api.openai.com (chat, embeddings, image
// generation, etc.). The admin API (api.openai.com/v1/organization/…)
// shares the host but doesn't transit through the agent's runtime
// flow — admin calls go through internal/providers/openai during
// the TUI mint/revoke flows, not via this proxy.
var HostToProvider = map[string]*Provider{
	"api.openai.com": {Name: "openai", Auth: AuthBearer, HasScopes: false},
}

// SuffixToProvider maps host suffixes (e.g. ".googleapis.com") to providers.
// Checked when no exact match is found in HostToProvider.
// suffixRule pairs a host suffix with a provider.
type suffixRule struct {
	Suffix   string
	Provider *Provider
}

// SuffixToProvider maps host suffixes (e.g. ".googleapis.com") to providers.
// Checked when no exact match is found in HostToProvider.
var SuffixToProvider = []suffixRule{
	{".googleapis.com", &Provider{Name: "google", Auth: AuthBearer, HasScopes: true}},
}

// ProviderForHost returns the provider config for a given host, or nil if unknown.
func ProviderForHost(host string) *Provider {
	// Exact match first.
	if p, ok := HostToProvider[host]; ok {
		return p
	}
	// Suffix match.
	for _, sp := range SuffixToProvider {
		if strings.HasSuffix(host, sp.Suffix) {
			return sp.Provider
		}
	}
	return nil
}
