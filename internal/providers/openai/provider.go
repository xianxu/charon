// Package openai implements providers.Provider for OpenAI using the
// Admin API endpoints under /v1/organization. See
// workshop/issues/000013-…md and the plan doc for the design context.
//
// Auth: every call carries `Authorization: Bearer <admin_key>`. The
// admin key itself lives at `_openai:admin` in the keychain (see
// internal/providers/keychain.go's AdminKeyStore); minted per-account
// keys live at `openai:<account>` as vault.Credential entries with
// Type=TypeAdminKey.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/xianxu/charon/internal/providers"
	"github.com/xianxu/charon/internal/vault"
)

// Name is the stable provider id used across vault.Credential.Provider
// values, keychain account prefixes, and the TUI's provider picker.
const Name = "openai"

// DefaultBaseURL is OpenAI's production Admin API. Tests override
// Provider.BaseURL with an httptest server URL.
const DefaultBaseURL = "https://api.openai.com"

// Provider implements providers.Provider for OpenAI.
type Provider struct {
	BaseURL string        // default DefaultBaseURL; tests override
	HTTP    *http.Client  // default has a sensible timeout
}

// New returns a Provider with sensible defaults for production use.
func New() *Provider {
	return &Provider{
		BaseURL: DefaultBaseURL,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *Provider) Name() string { return Name }
func (p *Provider) Type() string { return vault.TypeAdminKey }

// orgResponse is the shape of GET /v1/organization. We ignore fields
// charon doesn't use (created_at, billing info, etc.).
type orgResponse struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Title string `json:"title"` // some accounts surface a "title" instead of "name"
}

// projectResponse is the shape of a single project from GET / POST
// /v1/organization/projects.
type projectResponse struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Object string `json:"object"`
	Status string `json:"status"`
}

// projectsListResponse wraps the paginated list endpoint. Pagination
// is not exposed to callers in M2; the issue's MVP scope assumes
// project counts fit in a single page (<100). Future work can plumb
// has_more / first_id / last_id through if needed.
type projectsListResponse struct {
	Data    []projectResponse `json:"data"`
	HasMore bool              `json:"has_more"`
	Object  string            `json:"object"`
}

// apiKeyMintResponse is the shape returned from POST
// /v1/organization/projects/{id}/api_keys. The `value` field carries
// the full key material — captured here, never refetched.
type apiKeyMintResponse struct {
	ID            string `json:"id"`
	Object        string `json:"object"`
	Name          string `json:"name"`
	Value         string `json:"value"`          // full sk-… on creation only
	RedactedValue string `json:"redacted_value"` // sk-…xyz, on retrieve
	CreatedAt     int64  `json:"created_at"`
}

// errorResponse is OpenAI's standard error JSON wrapper.
type errorResponse struct {
	Err struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// DiscoverOrg returns the organization (id, name) for the given admin
// key. Used at admin-key paste time to capture the OrgID for storage
// and for the same-org-replace check.
func (p *Provider) DiscoverOrg(ctx context.Context, adminKey string) (orgID, orgName string, err error) {
	if adminKey == "" {
		return "", "", providers.ErrInvalidAdminKey
	}
	var out orgResponse
	if err := p.do(ctx, adminKey, http.MethodGet, "/v1/organization", nil, &out); err != nil {
		return "", "", err
	}
	if out.ID == "" {
		return "", "", fmt.Errorf("openai: /v1/organization returned empty id")
	}
	name := out.Name
	if name == "" {
		name = out.Title
	}
	return out.ID, name, nil
}

// ListProjects returns all projects accessible to this admin key. M2
// scope: single page (no pagination loop). Adequate for personal-
// gateway use; future work plumbs has_more if it ever matters.
func (p *Provider) ListProjects(ctx context.Context, adminKey string) ([]providers.Project, error) {
	var out projectsListResponse
	if err := p.do(ctx, adminKey, http.MethodGet, "/v1/organization/projects", nil, &out); err != nil {
		return nil, err
	}
	projects := make([]providers.Project, 0, len(out.Data))
	for _, pr := range out.Data {
		// Skip archived projects — they can't accept new keys; surfacing
		// them in the TUI's "select existing" picker would just lead to
		// downstream errors.
		if pr.Status == "archived" {
			continue
		}
		projects = append(projects, providers.Project{ID: pr.ID, Name: pr.Name})
	}
	return projects, nil
}

// CreateProject creates a new project upstream and returns its
// freshly-allocated ID + name. M2 sends only `{name}` — OpenAI accepts
// additional fields (description, etc.) we don't need yet.
func (p *Provider) CreateProject(ctx context.Context, adminKey, name string) (providers.Project, error) {
	body := map[string]string{"name": name}
	var out projectResponse
	if err := p.do(ctx, adminKey, http.MethodPost, "/v1/organization/projects", body, &out); err != nil {
		return providers.Project{}, err
	}
	if out.ID == "" {
		return providers.Project{}, fmt.Errorf("openai: create project returned empty id")
	}
	return providers.Project{ID: out.ID, Name: out.Name}, nil
}

// MintKey creates an API key inside the given project. The returned
// keyMaterial is captured here and MUST be persisted by the caller —
// OpenAI does not expose the full key material on subsequent reads.
func (p *Provider) MintKey(ctx context.Context, adminKey, projectID, keyName string) (keyID, keyMaterial string, err error) {
	body := map[string]string{"name": keyName}
	path := fmt.Sprintf("/v1/organization/projects/%s/api_keys", projectID)
	var out apiKeyMintResponse
	if err := p.do(ctx, adminKey, http.MethodPost, path, body, &out); err != nil {
		return "", "", err
	}
	if out.ID == "" || out.Value == "" {
		return "", "", fmt.Errorf("openai: mint returned id=%q value-empty=%v — API may have changed", out.ID, out.Value == "")
	}
	return out.ID, out.Value, nil
}

// RevokeKey deletes an upstream API key. Returns
// providers.ErrAlreadyRevoked if the upstream returns 404 — the key
// was already gone before charon got there. Caller proceeds with
// vault deletion regardless.
func (p *Provider) RevokeKey(ctx context.Context, adminKey, projectID, keyID string) error {
	path := fmt.Sprintf("/v1/organization/projects/%s/api_keys/%s", projectID, keyID)
	return p.do(ctx, adminKey, http.MethodDelete, path, nil, nil)
}

// do issues an authenticated request against the Admin API. Maps
// well-known status codes to provider-level sentinel errors so the TUI
// can branch on them without parsing OpenAI-specific JSON.
func (p *Provider) do(ctx context.Context, adminKey, method, path string, body any, out any) error {
	url := p.baseURL() + path

	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+adminKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.client().Do(req)
	if err != nil {
		return fmt.Errorf("openai %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	// Status mapping. 2xx → decode out; 401 → invalid admin key; 404
	// on DELETE → already revoked; anything else → wrap upstream
	// error message for the TUI.
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		if out == nil || len(respBody) == 0 {
			return nil
		}
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode response: %w (body=%q)", err, truncate(respBody, 200))
		}
		return nil
	case resp.StatusCode == http.StatusUnauthorized:
		return providers.ErrInvalidAdminKey
	case resp.StatusCode == http.StatusNotFound && method == http.MethodDelete:
		return providers.ErrAlreadyRevoked
	default:
		return parseUpstreamError(resp.StatusCode, respBody)
	}
}

func (p *Provider) baseURL() string {
	if p.BaseURL == "" {
		return DefaultBaseURL
	}
	return p.BaseURL
}

func (p *Provider) client() *http.Client {
	if p.HTTP == nil {
		return &http.Client{Timeout: 30 * time.Second}
	}
	return p.HTTP
}

// parseUpstreamError unwraps OpenAI's standard `{"error":{...}}`
// envelope into a Go error with the upstream message preserved.
// Falls back to a generic "status N" error if the body isn't
// recognizable JSON.
func parseUpstreamError(status int, body []byte) error {
	var er errorResponse
	if err := json.Unmarshal(body, &er); err == nil && er.Err.Message != "" {
		return fmt.Errorf("openai: %d %s: %s", status, er.Err.Type, er.Err.Message)
	}
	return fmt.Errorf("openai: %d (body=%q)", status, truncate(body, 200))
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// Compile-time guard: Provider must implement providers.Provider.
var _ providers.Provider = (*Provider)(nil)
