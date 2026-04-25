package proxy

import (
	"testing"
)

func TestProviderForHost_AllGoogleHosts(t *testing.T) {
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

func TestProviderForHost_UnknownReturnsNil(t *testing.T) {
	unknowns := []string{
		"example.com",
		"api.github.com",
		"notgoogleapis.com",
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
