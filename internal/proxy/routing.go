// Package proxy implements the Charon HTTPS forward proxy.
package proxy

import "fmt"

// AuthMethod defines how credentials are injected into requests.
type AuthMethod string

const (
	AuthBearer AuthMethod = "bearer" // Authorization: Bearer <token>
	// Future: AuthBasic, AuthHeader, AuthQuery, AuthAWSSigV4
)

// Provider describes a credential provider and how to inject auth.
type Provider struct {
	Name string
	Auth AuthMethod
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

// HostToProvider maps API hosts to credential providers.
var HostToProvider = map[string]*Provider{
	"gmail.googleapis.com":            {Name: "google", Auth: AuthBearer},
	"www.googleapis.com":              {Name: "google", Auth: AuthBearer},
	"oauth2.googleapis.com":           {Name: "google", Auth: AuthBearer},
	"people.googleapis.com":           {Name: "google", Auth: AuthBearer},
	"calendar-json.googleapis.com":    {Name: "google", Auth: AuthBearer},
	"sheets.googleapis.com":           {Name: "google", Auth: AuthBearer},
	"drive.googleapis.com":            {Name: "google", Auth: AuthBearer},
	"admin.googleapis.com":            {Name: "google", Auth: AuthBearer},
	"chat.googleapis.com":             {Name: "google", Auth: AuthBearer},
	"docs.googleapis.com":             {Name: "google", Auth: AuthBearer},
	"slides.googleapis.com":           {Name: "google", Auth: AuthBearer},
	"tasks.googleapis.com":            {Name: "google", Auth: AuthBearer},
	"youtube.googleapis.com":          {Name: "google", Auth: AuthBearer},
	"youtubeanalytics.googleapis.com": {Name: "google", Auth: AuthBearer},
	"storage.googleapis.com":          {Name: "google", Auth: AuthBearer},
}

// ProviderForHost returns the provider config for a given host, or nil if unknown.
func ProviderForHost(host string) *Provider {
	return HostToProvider[host]
}
