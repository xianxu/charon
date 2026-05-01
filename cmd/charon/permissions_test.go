package main

import (
	"reflect"
	"testing"

	"github.com/xianxu/charon/internal/vault"
	"github.com/xianxu/charon/internal/vault/memory"
)

// fixtureVault returns a memory vault with two google accounts and one
// dropbox account, each with distinct scope sets.
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

func TestPermissionsPayload_AllProviders(t *testing.T) {
	got, err := permissionsPayload(fixtureVault(t))
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

func TestPermissionsPayload_EmptyVault(t *testing.T) {
	got, err := permissionsPayload(memory.New())
	if err != nil {
		t.Fatalf("permissionsPayload: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty payload from empty vault, got %v", got)
	}
}

func TestPermissionsPayload_NilScopesNormalizedToEmptySlice(t *testing.T) {
	v := memory.New()
	v.Set(&vault.Credential{Provider: "google", Account: "noscopes@gmail.com"})
	got, err := permissionsPayload(v)
	if err != nil {
		t.Fatalf("permissionsPayload: %v", err)
	}
	scopes := got["google"]["noscopes@gmail.com"]
	if scopes == nil {
		t.Errorf("expected non-nil empty slice, got nil")
	}
	if len(scopes) != 0 {
		t.Errorf("expected empty scopes, got %v", scopes)
	}
}
