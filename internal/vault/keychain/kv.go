//go:build !darwin || !cgo

package keychain

import (
	"fmt"
	"os/exec"
	"strings"
)

// Low-level keychain key-value operations, usable beyond vault.Store
// (e.g., for storing CA certs). CLI fallback path; the darwin+cgo
// counterpart lives in kv_darwin.go.

// GetRaw reads a raw string value from the keychain.
func GetRaw(service, account string) (string, error) {
	out, err := exec.Command("security", "find-generic-password",
		"-s", service,
		"-a", account,
		"-w",
	).Output()
	if err != nil {
		return "", fmt.Errorf("keychain: not found %s/%s: %w", service, account, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// SetRaw writes a raw string value to the keychain.
// The -U flag on add-generic-password handles both create and update atomically.
func SetRaw(service, account, value string) error {
	return exec.Command("security", "add-generic-password",
		"-s", service,
		"-a", account,
		"-w", value,
		"-U",
	).Run()
}

// DeleteRaw removes a raw key/value entry. Returns nil if the entry
// doesn't exist (idempotent) — `security delete-generic-password`
// exits with status 44 (errSecItemNotFound) when the entry is missing,
// which we map to success. Other non-zero exits (locked keychain,
// permission denied, malformed args) are surfaced as errors so half-
// failed cascade deletes don't silently leave orphan entries.
func DeleteRaw(service, account string) error {
	cmd := exec.Command("security", "delete-generic-password",
		"-s", service,
		"-a", account,
	)
	if err := cmd.Run(); err != nil {
		// errSecItemNotFound → idempotent success. Anything else (locked
		// keychain, ACL denial, etc.) bubbles up so callers see real
		// failures.
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 44 {
			return nil
		}
		return fmt.Errorf("keychain delete %s/%s: %w", service, account, err)
	}
	return nil
}
