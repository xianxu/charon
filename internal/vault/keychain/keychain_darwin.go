//go:build darwin && cgo

// Primary darwin Store implementation: direct calls to the macOS Security
// framework. Replaces the `security` CLI shell-out (kept as fallback in
// keychain.go for !cgo / non-darwin builds).
//
// Get / List use github.com/keybase/go-keychain wrappers.
// Set + Delete go through acl_darwin.go's CGo helpers directly:
//   - Set attaches a SecAccess (ACL) to ServiceProd entries (keybase
//     doesn't expose SecAccess construction) and uses SecItemUpdate
//     for atomic upserts, preserving the ACL across token rotation.
//   - Delete tries SecItemDelete first and falls back to the legacy
//     SecKeychainItemDelete pair on errSecInvalidOwnerEdit (-25244),
//     which the modern API surfaces for items whose access object is
//     owned by another process — even without an explicit ACL.

package keychain

import (
	"encoding/json"
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

// NewWithService builds a Store bound to an explicit service
// namespace. Used by the security audit tool to inspect both
// `charon` and `charon-dev` namespaces from outside the running
// charon binary's own identity.
func NewWithService(service string) *Store {
	return &Store{service: service}
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

	// ServiceProd entries are written with an ACL bound to the current
	// process's designated requirement; ServiceDev entries skip the ACL
	// (dev iteration writes from many ephemeral binaries — go test, go
	// run — whose DRs don't match each other, so an ACL would lock dev
	// out of its own state).
	//
	// Both paths use SecItemUpdate-then-SecItemAdd for atomic upsert,
	// which preserves the ACL across token rotation on the prod path.
	withACL := s.service == ServiceProd
	return setGenericPassword(s.service, key, data, withACL)
}

func (s *Store) Delete(provider, account string) error {
	return deleteGenericPassword(s.service, keyName(provider, account))
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

