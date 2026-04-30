//go:build darwin && cgo

package keychain

import (
	"fmt"
	"os/exec"

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
// Routing by service namespace:
//   - ServiceProd: ACL pinned to the current process's designated
//     requirement via the legacy SecAccess path (setGenericPassword,
//     with_acl=true). Reads from any non-matching binary prompt.
//   - ServiceDev: shells out to `security add-generic-password -A`
//     ("allow any application without warning"). Required for `go run`
//     iteration: each invocation produces a different ephemeral
//     binary, and a default-ACL'd entry would prompt on every read
//     of a previously-written entry. -A is documented-insecure (any
//     app on the user's machine can read), which is fine for
//     ServiceDev — dev entries hold test secrets the user already
//     pastes interactively, never anything that ships.
//
// The dev path delete-then-adds (rather than -U upserts) so that an
// entry written previously by the C path (with restrictive default
// ACL) gets its ACL replaced cleanly.
func SetRaw(service, account, value string) error {
	if service == ServiceProd {
		return setGenericPassword(service, account, []byte(value), true)
	}
	return setRawPromptlessDev(service, account, value)
}

// setRawPromptlessDev writes a dev entry with `security -A` so any
// process on the user's machine can read it without a keychain prompt.
// Defensive against pre-existing entries with restrictive ACLs:
// delete-then-add ensures the ACL is replaced, not preserved.
func setRawPromptlessDev(service, account, value string) error {
	// Best-effort delete; missing-entry exit codes are fine.
	_ = exec.Command("security", "delete-generic-password",
		"-s", service,
		"-a", account,
	).Run()
	cmd := exec.Command("security", "add-generic-password",
		"-s", service,
		"-a", account,
		"-w", value,
		"-A", // allow any application — dev-only, fine for ServiceDev
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("keychain dev write %s/%s: %w (output: %s)", service, account, err, out)
	}
	return nil
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
