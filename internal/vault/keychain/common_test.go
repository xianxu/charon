package keychain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/xianxu/charon/internal/vault"
)

// Round-trip every payload through the keychain's storedCredential
// shape so additions to vault.Credential (like #14's GCP sidecar)
// don't get silently dropped on disk. Memory vault works in tests
// because it stores Credential directly; keychain's intermediate
// JSON struct must mirror every field.
func TestStoredCredentialRoundTripsAllPayloads(t *testing.T) {
	in := &vault.Credential{
		Type:         vault.TypeOAuth,
		Provider:     "google",
		Account:      "alice@gmail.com",
		AccessToken:  "at",
		RefreshToken: "rt",
		Expiry:       time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		Scopes:       []string{"openid", "https://www.googleapis.com/auth/cloud-platform"},
		GCP: &vault.GCPData{
			ProjectID:       "alice-charon",
			ProjectName:     "Alice Charon",
			VertexRegion:    "us-central1",
			CreatedByCharon: true,
			BillingEnabled:  true,
			UpdatedAt:       time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
		},
	}

	// Marshal → unmarshal mirrors the production write/read path.
	data, err := json.Marshal(fromCredential(in))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var sc storedCredential
	if err := json.Unmarshal(data, &sc); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	out := sc.toCredential()

	if out.GCP == nil {
		t.Fatalf("GCP sidecar lost in keychain round-trip; serialized form: %s", data)
	}
	if out.GCP.ProjectID != in.GCP.ProjectID {
		t.Errorf("ProjectID = %q, want %q", out.GCP.ProjectID, in.GCP.ProjectID)
	}
	if out.GCP.VertexRegion != in.GCP.VertexRegion {
		t.Errorf("VertexRegion = %q, want %q", out.GCP.VertexRegion, in.GCP.VertexRegion)
	}
	if !out.GCP.BillingEnabled {
		t.Error("BillingEnabled lost")
	}
	if !out.GCP.CreatedByCharon {
		t.Error("CreatedByCharon lost")
	}
}
