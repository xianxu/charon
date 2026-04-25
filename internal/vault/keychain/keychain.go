// Package keychain implements vault.Store using the macOS security CLI.
// Pure Go — no CGo required.
package keychain

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/xianxu/charon/internal/vault"
)

const serviceName = "charon"

// Store implements vault.Store using the macOS Keychain via the `security` CLI.
type Store struct{}

func New() *Store {
	return &Store{}
}

func keyName(provider, account string) string {
	return provider + ":" + account
}

// storedCredential is the JSON blob stored in keychain.
// For MVP, access_token is stored directly for manual testing.
// In production (M2+), only refresh_token is persisted; access_token is cached in memory.
type storedCredential struct {
	Provider     string    `json:"provider"`
	Account      string    `json:"account"`
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
	Scopes       []string  `json:"scopes,omitempty"`
}

func (s *Store) Get(provider, account string) (*vault.Credential, error) {
	key := keyName(provider, account)
	out, err := exec.Command("security", "find-generic-password",
		"-s", serviceName,
		"-a", key,
		"-w",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("credential not found for %s: %w", key, err)
	}

	var sc storedCredential
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &sc); err != nil {
		return nil, fmt.Errorf("corrupt credential for %s: %w", key, err)
	}

	return &vault.Credential{
		Provider:     sc.Provider,
		Account:      sc.Account,
		AccessToken:  sc.AccessToken,
		RefreshToken: sc.RefreshToken,
		Expiry:       sc.Expiry,
		Scopes:       sc.Scopes,
	}, nil
}

func (s *Store) Set(cred *vault.Credential) error {
	sc := storedCredential{
		Provider:     cred.Provider,
		Account:      cred.Account,
		AccessToken:  cred.AccessToken,
		RefreshToken: cred.RefreshToken,
		Expiry:       cred.Expiry,
		Scopes:       cred.Scopes,
	}
	data, err := json.Marshal(sc)
	if err != nil {
		return err
	}

	key := keyName(cred.Provider, cred.Account)

	// Delete existing entry if present (security CLI errors on duplicate).
	_ = exec.Command("security", "delete-generic-password",
		"-s", serviceName,
		"-a", key,
	).Run()

	return exec.Command("security", "add-generic-password",
		"-s", serviceName,
		"-a", key,
		"-w", string(data),
		"-U",
	).Run()
}

func (s *Store) Delete(provider, account string) error {
	key := keyName(provider, account)
	return exec.Command("security", "delete-generic-password",
		"-s", serviceName,
		"-a", key,
	).Run()
}

func (s *Store) List() ([]*vault.Credential, error) {
	out, err := exec.Command("security", "dump-keychain").Output()
	if err != nil {
		return nil, fmt.Errorf("failed to dump keychain: %w", err)
	}

	var creds []*vault.Credential
	var currentService, currentAccount string

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, `"svce"<blob>=`) {
			currentService = extractQuotedValue(line)
		}
		if strings.HasPrefix(line, `"acct"<blob>=`) {
			currentAccount = extractQuotedValue(line)
		}
		// When we have both and service matches, extract credential info.
		if currentService == serviceName && currentAccount != "" {
			parts := strings.SplitN(currentAccount, ":", 2)
			if len(parts) == 2 {
				creds = append(creds, &vault.Credential{
					Provider: parts[0],
					Account:  parts[1],
				})
			}
			currentService = ""
			currentAccount = ""
		}
	}

	return creds, nil
}

func extractQuotedValue(line string) string {
	// Format: "key"<blob>="value" or "key"<blob>=0xHEX "value"
	idx := strings.Index(line, "=")
	if idx < 0 {
		return ""
	}
	val := strings.TrimSpace(line[idx+1:])
	// Handle quoted value.
	if strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`) {
		return val[1 : len(val)-1]
	}
	// Handle hex + quoted: 0xABCD "value"
	if qIdx := strings.Index(val, `"`); qIdx >= 0 {
		end := strings.LastIndex(val, `"`)
		if end > qIdx {
			return val[qIdx+1 : end]
		}
	}
	return val
}
