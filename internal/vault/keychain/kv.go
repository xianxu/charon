package keychain

import (
	"fmt"
	"os/exec"
	"strings"
)

// Low-level keychain key-value operations, usable beyond vault.Store
// (e.g., for storing CA certs).

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
func SetRaw(service, account, value string) error {
	// Delete first, then add with -U to handle both create and update.
	_ = exec.Command("security", "delete-generic-password",
		"-s", service,
		"-a", account,
	).Run()

	return exec.Command("security", "add-generic-password",
		"-s", service,
		"-a", account,
		"-w", value,
		"-U",
	).Run()
}
