package main

import (
	"reflect"
	"testing"

	"github.com/xianxu/charon/internal/vault"
	"github.com/xianxu/charon/internal/vault/memory"
)

// fixtureVault returns a memory vault with two google accounts and one
// dropbox account, each with distinct scope sets, suitable for exercising
// the three permissionsPayload arg shapes.
func fixtureVault(t *testing.T) vault.Store {
	t.Helper()
	v := memory.New()
	for _, c := range []*vault.Credential{
		{
			Provider: "google",
			Account:  "alice@gmail.com",
			Scopes: []string{
				"openid",
				"https://www.googleapis.com/auth/userinfo.email",
				"https://www.googleapis.com/auth/gmail.readonly",
			},
		},
		{
			Provider: "google",
			Account:  "bob@gmail.com",
			Scopes: []string{
				"openid",
				"https://www.googleapis.com/auth/userinfo.email",
			},
		},
		{
			Provider: "dropbox",
			Account:  "alice@dropbox.com",
			Scopes:   []string{"files.content.read"},
		},
	} {
		if err := v.Set(c); err != nil {
			t.Fatalf("vault.Set: %v", err)
		}
	}
	return v
}

func TestPermissionsPayload_NoArgs_AllProviders(t *testing.T) {
	got, err := permissionsPayload(fixtureVault(t), nil)
	if err != nil {
		t.Fatalf("permissionsPayload: %v", err)
	}
	want := map[string]map[string][]string{
		"google": {
			"alice@gmail.com": {
				"openid",
				"https://www.googleapis.com/auth/userinfo.email",
				"https://www.googleapis.com/auth/gmail.readonly",
			},
			"bob@gmail.com": {
				"openid",
				"https://www.googleapis.com/auth/userinfo.email",
			},
		},
		"dropbox": {
			"alice@dropbox.com": {"files.content.read"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("payload mismatch.\n got=%v\nwant=%v", got, want)
	}
}

func TestPermissionsPayload_ProviderArg_OneProvider(t *testing.T) {
	got, err := permissionsPayload(fixtureVault(t), []string{"google"})
	if err != nil {
		t.Fatalf("permissionsPayload: %v", err)
	}
	want := map[string][]string{
		"alice@gmail.com": {
			"openid",
			"https://www.googleapis.com/auth/userinfo.email",
			"https://www.googleapis.com/auth/gmail.readonly",
		},
		"bob@gmail.com": {
			"openid",
			"https://www.googleapis.com/auth/userinfo.email",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("provider-filtered payload mismatch.\n got=%v\nwant=%v", got, want)
	}
}

func TestPermissionsPayload_ProviderAndAccount_ExactMatch(t *testing.T) {
	got, err := permissionsPayload(fixtureVault(t), []string{"google", "alice@gmail.com"})
	if err != nil {
		t.Fatalf("permissionsPayload: %v", err)
	}
	want := []string{
		"openid",
		"https://www.googleapis.com/auth/userinfo.email",
		"https://www.googleapis.com/auth/gmail.readonly",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("exact-match payload mismatch.\n got=%v\nwant=%v", got, want)
	}
}

func TestPermissionsPayload_ProviderAndAccount_MissingErrors(t *testing.T) {
	_, err := permissionsPayload(fixtureVault(t), []string{"google", "nobody@example.com"})
	if err == nil {
		t.Error("expected error for missing account, got nil")
	}
}

func TestPermissionsPayload_UnknownProvider_EmptyMap(t *testing.T) {
	got, err := permissionsPayload(fixtureVault(t), []string{"linkedin"})
	if err != nil {
		t.Fatalf("permissionsPayload: %v", err)
	}
	m, ok := got.(map[string][]string)
	if !ok {
		t.Fatalf("expected map[string][]string, got %T", got)
	}
	if len(m) != 0 {
		t.Errorf("expected empty map for unknown provider, got %v", m)
	}
}

func TestPermissionsPayload_EmptyVault(t *testing.T) {
	got, err := permissionsPayload(memory.New(), nil)
	if err != nil {
		t.Fatalf("permissionsPayload: %v", err)
	}
	m, ok := got.(map[string]map[string][]string)
	if !ok {
		t.Fatalf("expected nested map, got %T", got)
	}
	if len(m) != 0 {
		t.Errorf("expected empty payload from empty vault, got %v", m)
	}
}

func TestPermissionsPayload_NilScopesNormalizedToEmptySlice(t *testing.T) {
	v := memory.New()
	// Credential with no scopes (nil slice). JSON output should be [] not null.
	v.Set(&vault.Credential{Provider: "google", Account: "noscopes@gmail.com"})
	got, err := permissionsPayload(v, []string{"google", "noscopes@gmail.com"})
	if err != nil {
		t.Fatalf("permissionsPayload: %v", err)
	}
	scopes, ok := got.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", got)
	}
	if scopes == nil {
		t.Errorf("expected non-nil empty slice, got nil")
	}
	if len(scopes) != 0 {
		t.Errorf("expected empty scopes, got %v", scopes)
	}
}
