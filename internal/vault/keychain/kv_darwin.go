//go:build darwin && cgo

package keychain

import (
	"fmt"

	gokeychain "github.com/keybase/go-keychain"
)

// Low-level keychain key-value operations, usable beyond vault.Store
// (e.g., for storing CA certs). darwin+cgo path; CLI fallback in kv.go.

// GetRaw reads a raw string value from the keychain.
//
// No TrimSpace here — the Security framework returns exact stored bytes.
// The CLI counterpart in kv.go trims because `security -w` appends a
// trailing newline. Round-trip via SetRaw→GetRaw is bytewise identical
// on either backend.
func GetRaw(service, account string) (string, error) {
	data, err := gokeychain.GetGenericPassword(service, account, "", "")
	if err != nil {
		return "", fmt.Errorf("keychain: %s/%s: %w", service, account, err)
	}
	if data == nil {
		return "", fmt.Errorf("keychain: not found %s/%s", service, account)
	}
	return string(data), nil
}

// SetRaw writes a raw string value to the keychain.
//
// Atomic upsert via SecItemUpdate-then-SecItemAdd (see setGenericPassword).
// Routes ACL based on service name: ServiceProd entries (signed binary)
// get an ACL bound to the current process; ServiceDev entries don't,
// since dev iteration writes from many ephemeral binaries with
// non-matching designated requirements.
func SetRaw(service, account, value string) error {
	withACL := service == ServiceProd
	return setGenericPassword(service, account, []byte(value), withACL)
}

// DeleteRaw removes a raw key/value entry. Idempotent — returns nil if
// the entry doesn't exist, matching the CLI fallback's semantics so
// callers don't have to distinguish "didn't exist" from "deleted".
func DeleteRaw(service, account string) error {
	err := gokeychain.DeleteGenericPasswordItem(service, account)
	if err == gokeychain.ErrorItemNotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("keychain delete: %s/%s: %w", service, account, err)
	}
	return nil
}
