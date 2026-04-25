package proxy

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// BuildCABundle creates a combined PEM file with system root CAs + Charon CA.
// Returns the path to the bundle file.
func BuildCABundle(dir string, charonCAPEM []byte) (string, error) {
	bundlePath := filepath.Join(dir, "ca-bundle.pem")

	systemPEM, err := loadSystemCAs()
	if err != nil {
		// If we can't load system CAs, just use our CA alone.
		// Non-intercepted hosts use passthrough tunneling anyway,
		// so they do their own TLS with system CAs.
		systemPEM = nil
	}

	var bundle []byte
	if len(systemPEM) > 0 {
		bundle = append(bundle, systemPEM...)
		if !strings.HasSuffix(string(systemPEM), "\n") {
			bundle = append(bundle, '\n')
		}
	}
	bundle = append(bundle, charonCAPEM...)

	if err := os.WriteFile(bundlePath, bundle, 0644); err != nil {
		return "", err
	}
	return bundlePath, nil
}

func loadSystemCAs() ([]byte, error) {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("security", "find-certificate", "-a", "-p",
			"/System/Library/Keychains/SystemRootCertificates.keychain").Output()
	default:
		// Common Linux CA bundle paths.
		for _, path := range []string{
			"/etc/ssl/certs/ca-certificates.crt",
			"/etc/pki/tls/certs/ca-bundle.crt",
			"/etc/ssl/ca-bundle.pem",
		} {
			if data, err := os.ReadFile(path); err == nil {
				return data, nil
			}
		}
		return nil, os.ErrNotExist
	}
}
