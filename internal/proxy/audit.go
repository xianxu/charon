package proxy

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AuditEntry represents a single proxied request in the audit log.
type AuditEntry struct {
	Timestamp  time.Time `json:"ts"`
	Method     string    `json:"method"`
	Host       string    `json:"host"`
	Path       string    `json:"path"`
	StatusCode int       `json:"status"`
	LatencyMs  int64     `json:"latency_ms"`
	Provider   string    `json:"provider,omitempty"`
	Account    string    `json:"account,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// AuditLog writes append-only JSON lines to a log file.
type AuditLog struct {
	mu sync.Mutex
	w  io.WriteCloser
}

// NewAuditLog creates an audit log writer. If path is empty, uses ~/.config/charon/audit.log.
func NewAuditLog(path string) (*AuditLog, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir := filepath.Join(home, ".config", "charon")
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		path = filepath.Join(dir, "audit.log")
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	return &AuditLog{w: f}, nil
}

// Log writes an audit entry.
func (a *AuditLog) Log(entry AuditEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	data, _ := json.Marshal(entry)
	data = append(data, '\n')
	_, _ = a.w.Write(data)
}

// Close closes the underlying file.
func (a *AuditLog) Close() error {
	return a.w.Close()
}

// NopAuditLog returns an audit log that discards entries (for testing).
func NopAuditLog() *AuditLog {
	return &AuditLog{w: nopWriteCloser{}}
}

type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }
