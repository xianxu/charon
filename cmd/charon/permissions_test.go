package main

import (
	"reflect"
	"testing"
	"time"

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
	want := map[string]map[string]AccountPermissions{
		"google": {
			"alice@gmail.com": {Scopes: []string{
				"openid",
				"https://www.googleapis.com/auth/userinfo.email",
				"https://www.googleapis.com/auth/gmail.readonly",
			}},
			"bob@gmail.com": {Scopes: []string{
				"openid",
				"https://www.googleapis.com/auth/userinfo.email",
			}},
		},
		"dropbox": {
			"alice@dropbox.com": {Scopes: []string{"files.content.read"}},
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
	entry := got["google"]["noscopes@gmail.com"]
	if entry.Scopes == nil {
		t.Errorf("expected non-nil empty slice, got nil")
	}
	if len(entry.Scopes) != 0 {
		t.Errorf("expected empty scopes, got %v", entry.Scopes)
	}
	if entry.GCP != nil {
		t.Errorf("expected no GCP payload, got %+v", entry.GCP)
	}
}

func TestPermissionsPayload_GCPSurfacesWhenPresent(t *testing.T) {
	v := memory.New()
	v.Set(&vault.Credential{
		Provider: "google",
		Account:  "alice@gmail.com",
		Scopes: []string{
			"openid",
			"https://www.googleapis.com/auth/cloud-platform",
		},
		GCP: &vault.GCPData{
			ProjectID:       "alice-charon",
			ProjectName:     "Alice Charon",
			VertexRegion:    "us-central1",
			CreatedByCharon: true,
			BillingEnabled:  true,
			UpdatedAt:       time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		},
	})
	got, err := permissionsPayload(v)
	if err != nil {
		t.Fatalf("permissionsPayload: %v", err)
	}
	entry := got["google"]["alice@gmail.com"]
	if entry.GCP == nil {
		t.Fatal("expected GCP payload to be surfaced")
	}
	if entry.GCP.ProjectID != "alice-charon" {
		t.Errorf("ProjectID = %q, want alice-charon", entry.GCP.ProjectID)
	}
	if entry.GCP.VertexRegion != "us-central1" {
		t.Errorf("VertexRegion = %q", entry.GCP.VertexRegion)
	}
	if !entry.GCP.BillingEnabled {
		t.Error("expected BillingEnabled=true")
	}
}
