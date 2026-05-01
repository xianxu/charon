package proxy

import (
	"net/http"
	"testing"
)

func TestProviderForHost_GoogleSuffix(t *testing.T) {
	// All *.googleapis.com hosts should match via suffix rule.
	googleHosts := []string{
		"gmail.googleapis.com",
		"www.googleapis.com",
		"oauth2.googleapis.com",
		"people.googleapis.com",
		"calendar-json.googleapis.com",
		"sheets.googleapis.com",
		"drive.googleapis.com",
		"admin.googleapis.com",
		"chat.googleapis.com",
		"docs.googleapis.com",
		"slides.googleapis.com",
		"tasks.googleapis.com",
		"youtube.googleapis.com",
		"youtubeanalytics.googleapis.com",
		"storage.googleapis.com",
		"some-future-api.googleapis.com", // not in any explicit list — suffix catches it
		// Gemini-via-Vertex paths (#000014 M2): suffix rule covers
		// regional Vertex hosts. Note: AI Studio
		// (generativelanguage.googleapis.com) is now an exact-match
		// override with URL-param auth — see
		// TestProviderForHost_AIStudioGetsURLParamAuth.
		"us-central1-aiplatform.googleapis.com",
		"europe-west4-aiplatform.googleapis.com",
		"apikeys.googleapis.com", // used by M3/M4 (mint API)
	}
	for _, host := range googleHosts {
		p := ProviderForHost(host)
		if p == nil {
			t.Errorf("ProviderForHost(%q) = nil, want google provider", host)
			continue
		}
		if p.Name != "google" {
			t.Errorf("ProviderForHost(%q).Name = %q, want %q", host, p.Name, "google")
		}
		if p.Auth != AuthBearer {
			t.Errorf("ProviderForHost(%q).Auth = %q, want %q", host, p.Auth, AuthBearer)
		}
	}
}

func TestProviderForHost_OpenAI(t *testing.T) {
	p := ProviderForHost("api.openai.com")
	if p == nil {
		t.Fatal("api.openai.com should resolve to the openai provider")
	}
	if p.Name != "openai" {
		t.Errorf("Name = %q, want openai", p.Name)
	}
	if p.Auth != AuthBearer {
		t.Errorf("Auth = %q, want bearer", p.Auth)
	}
	if p.HasScopes {
		t.Error("openai is admin-key — HasScopes should be false")
	}
}

func TestProviderForHost_GoogleHasScopes(t *testing.T) {
	p := ProviderForHost("gmail.googleapis.com")
	if p == nil || !p.HasScopes {
		t.Error("google (OAuth) provider should have HasScopes=true")
	}
}

func TestProviderForHost_ExactMatchOverridesSuffix(t *testing.T) {
	// Add an exact match that overrides the suffix rule.
	HostToProvider["special.googleapis.com"] = &Provider{Name: "special", Auth: AuthBearer}
	defer delete(HostToProvider, "special.googleapis.com")

	p := ProviderForHost("special.googleapis.com")
	if p == nil || p.Name != "special" {
		t.Errorf("exact match should take precedence, got %+v", p)
	}
}

func TestProviderForHost_UnknownReturnsNil(t *testing.T) {
	unknowns := []string{
		"example.com",
		"api.github.com",
		"notgoogleapis.com", // no dot prefix — should NOT match ".googleapis.com"
		"",
	}
	for _, host := range unknowns {
		if p := ProviderForHost(host); p != nil {
			t.Errorf("ProviderForHost(%q) = %+v, want nil", host, p)
		}
	}
}

// reqWithURL builds a minimal *http.Request for InjectAuth tests.
func reqWithURL(t *testing.T, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestInjectAuth_Bearer(t *testing.T) {
	p := &Provider{Name: "test", Auth: AuthBearer}
	req := reqWithURL(t, "https://example.com/x")
	if err := p.InjectAuth(req, "my-token"); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer my-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer my-token")
	}
}

func TestInjectAuth_DefaultIsBearer(t *testing.T) {
	p := &Provider{Name: "test", Auth: ""}
	req := reqWithURL(t, "https://example.com/x")
	if err := p.InjectAuth(req, "tok"); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer tok" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer tok")
	}
}

func TestInjectAuth_URLParamKey(t *testing.T) {
	p := &Provider{Name: "google-aistudio", Auth: AuthURLParamKey}
	req := reqWithURL(t, "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent")
	if err := p.InjectAuth(req, "AIzaSy_FAKE"); err != nil {
		t.Fatal(err)
	}
	if got := req.URL.Query().Get("key"); got != "AIzaSy_FAKE" {
		t.Errorf("URL key param = %q, want %q", got, "AIzaSy_FAKE")
	}
	// Header should NOT carry an Authorization for URL-param auth.
	if h := req.Header.Get("Authorization"); h != "" {
		t.Errorf("URL-param auth must not set Authorization header, got %q", h)
	}
}

func TestInjectAuth_URLParamKeyPreservesExistingQuery(t *testing.T) {
	p := &Provider{Name: "google-aistudio", Auth: AuthURLParamKey}
	req := reqWithURL(t, "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent?alt=json&prettyPrint=true")
	if err := p.InjectAuth(req, "AIzaSy_FAKE"); err != nil {
		t.Fatal(err)
	}
	q := req.URL.Query()
	if q.Get("key") != "AIzaSy_FAKE" {
		t.Errorf("key not attached, got %v", req.URL.RawQuery)
	}
	if q.Get("alt") != "json" || q.Get("prettyPrint") != "true" {
		t.Errorf("existing query params dropped: %v", req.URL.RawQuery)
	}
}

func TestInjectAuth_UnsupportedMethod(t *testing.T) {
	p := &Provider{Name: "test", Auth: "magic"}
	if err := p.InjectAuth(reqWithURL(t, "https://example.com/x"), "tok"); err == nil {
		t.Error("expected error for unsupported auth method")
	}
}

func TestProviderForHost_AIStudioGetsURLParamAuth(t *testing.T) {
	p := ProviderForHost("generativelanguage.googleapis.com")
	if p == nil {
		t.Fatal("expected AI Studio host to resolve")
	}
	if p.Auth != AuthURLParamKey {
		t.Errorf("Auth = %q, want %q", p.Auth, AuthURLParamKey)
	}
	if p.HasScopes {
		t.Error("AI Studio routes have no scope semantics")
	}
	if p.VaultName() != "google" {
		t.Errorf("VaultName = %q, want google", p.VaultName())
	}
	if p.Name != "google-aistudio" {
		t.Errorf("Name = %q, want google-aistudio", p.Name)
	}
}
