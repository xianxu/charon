package proxy

import (
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

func TestInjectAuth_Bearer(t *testing.T) {
	p := &Provider{Name: "test", Auth: AuthBearer}
	var gotKey, gotValue string
	err := p.InjectAuth(func(k, v string) { gotKey = k; gotValue = v }, "my-token")
	if err != nil {
		t.Fatal(err)
	}
	if gotKey != "Authorization" {
		t.Errorf("expected key 'Authorization', got %q", gotKey)
	}
	if gotValue != "Bearer my-token" {
		t.Errorf("expected 'Bearer my-token', got %q", gotValue)
	}
}

func TestInjectAuth_DefaultIsBearer(t *testing.T) {
	p := &Provider{Name: "test", Auth: ""}
	var gotValue string
	err := p.InjectAuth(func(k, v string) { gotValue = v }, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if gotValue != "Bearer tok" {
		t.Errorf("expected 'Bearer tok', got %q", gotValue)
	}
}

func TestInjectAuth_UnsupportedMethod(t *testing.T) {
	p := &Provider{Name: "test", Auth: "magic"}
	err := p.InjectAuth(func(k, v string) {}, "tok")
	if err == nil {
		t.Error("expected error for unsupported auth method")
	}
}
