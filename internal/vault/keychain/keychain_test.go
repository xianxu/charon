//go:build integration

// These tests hit the real macOS Keychain.
// Run with: go test -tags integration ./internal/vault/keychain/
//
// They create and delete entries under the "charon" service name.

package keychain

import (
	"testing"

	"github.com/xianxu/charon/internal/vault"
)

const testProvider = "charon-test"
const testAccount = "integration-test@example.com"

func cleanup(s *Store) {
	_ = s.Delete(testProvider, testAccount)
}

func TestKeychainSetAndGet(t *testing.T) {
	s := New()
	defer cleanup(s)

	err := s.Set(&vault.Credential{
		Provider:    testProvider,
		Account:     testAccount,
		AccessToken: "test-access-token",
		Scopes:      []string{"email", "profile"},
	})
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	cred, err := s.Get(testProvider, testAccount)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if cred.Provider != testProvider {
		t.Errorf("Provider = %q, want %q", cred.Provider, testProvider)
	}
	if cred.Account != testAccount {
		t.Errorf("Account = %q, want %q", cred.Account, testAccount)
	}
	if cred.AccessToken != "test-access-token" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "test-access-token")
	}
	if len(cred.Scopes) != 2 || cred.Scopes[0] != "email" {
		t.Errorf("Scopes = %v, want [email profile]", cred.Scopes)
	}
}

func TestKeychainOverwrite(t *testing.T) {
	s := New()
	defer cleanup(s)

	_ = s.Set(&vault.Credential{
		Provider:    testProvider,
		Account:     testAccount,
		AccessToken: "old-token",
	})
	_ = s.Set(&vault.Credential{
		Provider:    testProvider,
		Account:     testAccount,
		AccessToken: "new-token",
	})

	cred, err := s.Get(testProvider, testAccount)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if cred.AccessToken != "new-token" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "new-token")
	}
}

func TestKeychainDelete(t *testing.T) {
	s := New()
	defer cleanup(s)

	_ = s.Set(&vault.Credential{
		Provider:    testProvider,
		Account:     testAccount,
		AccessToken: "to-delete",
	})

	if err := s.Delete(testProvider, testAccount); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err := s.Get(testProvider, testAccount)
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}

func TestKeychainGetNotFound(t *testing.T) {
	s := New()
	_, err := s.Get("nonexistent", "nobody@example.com")
	if err == nil {
		t.Error("expected error for missing credential")
	}
}

func TestKeychainList(t *testing.T) {
	s := New()
	defer cleanup(s)

	_ = s.Set(&vault.Credential{
		Provider:    testProvider,
		Account:     testAccount,
		AccessToken: "list-test",
	})

	creds, err := s.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	found := false
	for _, c := range creds {
		if c.Provider == testProvider && c.Account == testAccount {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("List did not include test credential (got %d entries)", len(creds))
	}
}
