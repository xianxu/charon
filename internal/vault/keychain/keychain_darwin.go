//go:build darwin && cgo

// Primary darwin Store implementation: direct calls to the macOS Security
// framework via github.com/keybase/go-keychain. Replaces the `security` CLI
// shell-out (kept as fallback in keychain.go for !cgo / non-darwin builds).
//
// M2 scope: parity with the CLI backend — Get/Set/Delete/List, no ACL writes.
// ACL plumbing arrives in M4. Service-name selection (`charon` vs
// `charon-dev` based on the binary's signing state) arrives in M3.

package keychain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	gokeychain "github.com/keybase/go-keychain"
	"github.com/xianxu/charon/internal/vault"
)

// Store implements vault.Store via the macOS Security framework.
//
// service is the keychain service-name namespace, snapshotted from
// ResolveServiceName at construction. ServiceProd for a signed binary,
// ServiceDev for unsigned/dev — see service.go.
type Store struct {
	service string
}

func New() *Store {
	return &Store{service: ResolveServiceName()}
}

func (s *Store) Get(provider, account string) (*vault.Credential, error) {
	key := keyName(provider, account)
	data, err := gokeychain.GetGenericPassword(s.service, key, "", "")
	if err != nil {
		return nil, fmt.Errorf("keychain Get %s: %w", key, err)
	}
	if data == nil {
		return nil, fmt.Errorf("credential not found for %s", key)
	}
	var sc storedCredential
	if err := json.Unmarshal(data, &sc); err != nil {
		return nil, fmt.Errorf("corrupt credential for %s: %w", key, err)
	}
	return sc.toCredential(), nil
}

func (s *Store) Set(cred *vault.Credential) error {
	data, err := json.Marshal(fromCredential(cred))
	if err != nil {
		return err
	}
	key := keyName(cred.Provider, cred.Account)

	item := gokeychain.NewItem()
	item.SetSecClass(gokeychain.SecClassGenericPassword)
	item.SetService(s.service)
	item.SetAccount(key)
	item.SetData(data)
	item.SetSynchronizable(gokeychain.SynchronizableNo)
	item.SetAccessible(gokeychain.AccessibleWhenUnlocked)

	// Replace-on-write: AddItem fails on duplicate, so delete first.
	// Idempotent — DeleteGenericPasswordItem on a missing key is a no-op
	// at the Security framework level (returns errSecItemNotFound which
	// we ignore).
	if err := gokeychain.DeleteGenericPasswordItem(s.service, key); err != nil &&
		!isItemNotFound(err) {
		return fmt.Errorf("keychain Set (pre-delete) %s: %w", key, err)
	}
	if err := gokeychain.AddItem(item); err != nil {
		return fmt.Errorf("keychain Set %s: %w", key, err)
	}
	return nil
}

func (s *Store) Delete(provider, account string) error {
	key := keyName(provider, account)
	if err := gokeychain.DeleteGenericPasswordItem(s.service, key); err != nil &&
		!isItemNotFound(err) {
		return fmt.Errorf("keychain Delete %s: %w", key, err)
	}
	return nil
}

func (s *Store) List() ([]*vault.Credential, error) {
	accounts, err := gokeychain.GetGenericPasswordAccounts(s.service)
	if err != nil {
		return nil, fmt.Errorf("keychain List: %w", err)
	}
	creds := make([]*vault.Credential, 0, len(accounts))
	for _, key := range accounts {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			continue
		}
		// Skip internal namespaces (e.g. "_ca:cert" — CA storage).
		if strings.HasPrefix(parts[0], "_") {
			continue
		}
		creds = append(creds, &vault.Credential{
			Provider: parts[0],
			Account:  parts[1],
		})
	}
	return creds, nil
}

// isItemNotFound matches the keybase/go-keychain ErrorItemNotFound sentinel.
// The library's Error type is an integer wrapping OSStatus; errors.Is doesn't
// work because Error doesn't implement an Is method, so we type-assert.
func isItemNotFound(err error) bool {
	var ke gokeychain.Error
	if errors.As(err, &ke) {
		return ke == gokeychain.ErrorItemNotFound
	}
	return false
}
