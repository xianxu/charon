package memory

import (
	"testing"

	"github.com/xianxu/charon/internal/vault"
)

func TestStoreGetSetDelete(t *testing.T) {
	s := New()

	// Set.
	err := s.Set(&vault.Credential{
		Provider:    "google",
		Account:     "user@gmail.com",
		AccessToken: "tok-123",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get.
	cred, err := s.Get("google", "user@gmail.com")
	if err != nil {
		t.Fatal(err)
	}
	if cred.AccessToken != "tok-123" {
		t.Errorf("got token %q, want %q", cred.AccessToken, "tok-123")
	}

	// List (should not include access tokens).
	creds, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 1 {
		t.Fatalf("expected 1 credential, got %d", len(creds))
	}
	if creds[0].AccessToken != "" {
		t.Error("List should not return access tokens")
	}

	// Delete.
	if err := s.Delete("google", "user@gmail.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("google", "user@gmail.com"); err == nil {
		t.Error("expected error after delete")
	}
}

func TestGetNotFound(t *testing.T) {
	s := New()
	_, err := s.Get("nope", "nope")
	if err == nil {
		t.Error("expected error for missing credential")
	}
}
