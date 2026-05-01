// Package proxy implements the Charon HTTPS forward proxy.
package proxy

import (
	"fmt"
	"net/http"
	"strings"
)

// AuthMethod defines how credentials are injected into requests.
type AuthMethod string

const (
	AuthBearer      AuthMethod = "bearer"        // Authorization: Bearer <token>
	AuthURLParamKey AuthMethod = "url_param_key" // ?key=<token> on URL (Google AI Studio)
	// Future: AuthBasic, AuthHeader, AuthAWSSigV4
)

// Provider describes a credential provider and how to inject auth.
//
// HasScopes controls whether X-Charon-Scope is honored for routes
// to this provider. OAuth providers (Google) have scope semantics
// and consume the header. Admin-key providers (OpenAI) and catalog
// providers have no scope concept — the header is silently ignored
// on their routes per the agent-protocol contract. Charon strips
// the header from outbound requests in either case.
//
// VaultProvider is the provider name to look up credentials under in
// the vault. Empty means use Name (the typical case). Set explicitly
// when a routing provider piggybacks on another provider's credential
// — e.g. the AI Studio route ("google-aistudio") looks up its key
// from the underlying Google credential ("google").
type Provider struct {
	Name          string
	Auth          AuthMethod
	HasScopes     bool
	VaultProvider string
}

// VaultName returns the provider name to use for vault lookups,
// defaulting to Name when VaultProvider is unset.
func (p *Provider) VaultName() string {
	if p.VaultProvider != "" {
		return p.VaultProvider
	}
	return p.Name
}

// InjectAuth attaches credentials to req per the provider's AuthMethod.
// AuthBearer sets the Authorization header; AuthURLParamKey appends
// `?key=<token>` to the URL (Google AI Studio's auth model). The
// proxy calls this once per request after resolving the credential.
func (p *Provider) InjectAuth(req *http.Request, token string) error {
	switch p.Auth {
	case AuthBearer, "": // default to bearer
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	case AuthURLParamKey:
		q := req.URL.Query()
		q.Set("key", token)
		req.URL.RawQuery = q.Encode()
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
	// AI Studio runs on its own host with API-key URL-param auth,
	// distinct from the rest of the Google universe (which uses
	// OAuth bearer). Exact-match takes precedence over the
	// .googleapis.com suffix rule below. Credentials live under
	// the "google" namespace; the URL-param-attached key comes from
	// cred.AIStudio.KeyMaterial.
	"generativelanguage.googleapis.com": {
		Name:          "google-aistudio",
		Auth:          AuthURLParamKey,
		HasScopes:     false,
		VaultProvider: "google",
	},
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
