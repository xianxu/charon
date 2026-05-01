package main

import (
	"bytes"
	"encoding/json"
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

// AI Studio surfaces in the manifest as a redacted ref — UID + display
// name + project_id only. The actual KeyMaterial must NOT appear:
// keeping the key invisible to agents is charon's job. Regression
// guard for the specific oversight where M4 added cred.AIStudio
// storage but forgot the projection on the manifest side.
func TestPermissionsPayload_AIStudioSurfacedRedacted(t *testing.T) {
	v := memory.New()
	v.Set(&vault.Credential{
		Provider: "google",
		Account:  "alice@gmail.com",
		Scopes:   []string{"https://www.googleapis.com/auth/cloud-platform"},
		AIStudio: &vault.AIStudioData{
			Name:        "projects/alice-charon/locations/global/keys/uid-1",
			UID:         "uid-1",
			DisplayName: "charon-aistudio",
			KeyMaterial: "AIzaSy_THIS_IS_THE_SECRET",
			ProjectID:   "alice-charon",
			CreatedAt:   time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		},
	})
	got, err := permissionsPayload(v)
	if err != nil {
		t.Fatalf("permissionsPayload: %v", err)
	}
	entry := got["google"]["alice@gmail.com"]
	if entry.AIStudio == nil {
		t.Fatal("expected AIStudio ref in manifest")
	}
	if entry.AIStudio.UID != "uid-1" {
		t.Errorf("UID = %q", entry.AIStudio.UID)
	}
	if entry.AIStudio.ProjectID != "alice-charon" {
		t.Errorf("ProjectID = %q", entry.AIStudio.ProjectID)
	}

	// The secret must not appear anywhere in the JSON shape.
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if bytes.Contains(b, []byte("AIzaSy_THIS_IS_THE_SECRET")) {
		t.Errorf("KeyMaterial leaked into manifest JSON:\n%s", b)
	}
	if bytes.Contains(b, []byte("key_material")) {
		t.Errorf("manifest should not have a key_material field at all:\n%s", b)
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
