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

	// Routing by namespace:
	//   - ServiceProd: SecAccess pinned to current process's designated
	//     requirement via setGenericPassword(withACL=true). Atomic
	//     upsert preserves the ACL across token rotation.
	//   - ServiceDev: shells out to `security add-generic-password -A`
	//     (any-application access). Without -A, macOS attaches a
	//     default SecAccess pinned to the writing binary's identity;
	//     `go run` produces a different ephemeral binary on every
	//     invocation, so subsequent reads from a fresh ephemeral
	//     binary prompt. -A is documented-insecure (any app on the
	//     user's machine can read), which is fine for ServiceDev —
	//     dev entries hold test secrets the user pastes interactively
	//     into an unsigned binary, never anything that ships.
	//
	// kv_darwin.SetRaw applies the same routing for raw entries
	// (admin key + meta) used by AdminKeyStore.
	if s.service == ServiceProd {
		return setGenericPassword(s.service, key, data, true)
	}
	return setRawPromptlessDev(s.service, key, string(data))
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

