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

// SetRaw writes a raw string value to the keychain. Replace-on-write.
func SetRaw(service, account, value string) error {
	item := gokeychain.NewItem()
	item.SetSecClass(gokeychain.SecClassGenericPassword)
	item.SetService(service)
	item.SetAccount(account)
	item.SetData([]byte(value))
	item.SetSynchronizable(gokeychain.SynchronizableNo)
	item.SetAccessible(gokeychain.AccessibleWhenUnlocked)

	if err := gokeychain.DeleteGenericPasswordItem(service, account); err != nil &&
		!isItemNotFound(err) {
		return fmt.Errorf("keychain SetRaw (pre-delete) %s/%s: %w", service, account, err)
	}
	if err := gokeychain.AddItem(item); err != nil {
		return fmt.Errorf("keychain SetRaw %s/%s: %w", service, account, err)
	}
	return nil
}
