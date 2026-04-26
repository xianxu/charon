package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/xianxu/charon/internal/vault"
	"github.com/xianxu/charon/internal/vault/memory"
)

func TestScopeTracker_Track(t *testing.T) {
	st := NewScopeTracker(100, 24*time.Hour)

	st.Track("google", "user@gmail.com", []string{"calendar.readonly"})
	st.Track("google", "user@gmail.com", []string{"calendar.readonly"}) // increment count

	denials := st.Denials("", "")
	if len(denials) != 1 {
		t.Fatalf("expected 1 denial, got %d", len(denials))
	}
	if denials[0].Count != 2 {
		t.Errorf("expected count 2, got %d", denials[0].Count)
	}
	if denials[0].Scope != "calendar.readonly" {
		t.Errorf("expected scope 'calendar.readonly', got %q", denials[0].Scope)
	}
}

func TestScopeTracker_MultipleScopes(t *testing.T) {
	st := NewScopeTracker(100, 24*time.Hour)

	st.Track("google", "user@gmail.com", []string{"calendar.readonly", "drive.readonly"})

	denials := st.Denials("", "")
	if len(denials) != 2 {
		t.Fatalf("expected 2 denials, got %d", len(denials))
	}
}

func TestScopeTracker_FilterByProvider(t *testing.T) {
	st := NewScopeTracker(100, 24*time.Hour)

	st.Track("google", "user@gmail.com", []string{"calendar.readonly"})
	st.Track("dropbox", "user@gmail.com", []string{"files.content.read"})

	google := st.Denials("google", "")
	if len(google) != 1 {
		t.Fatalf("expected 1 google denial, got %d", len(google))
	}
	if google[0].Provider != "google" {
		t.Errorf("expected provider 'google', got %q", google[0].Provider)
	}
}

func TestScopeTracker_FilterByAccount(t *testing.T) {
	st := NewScopeTracker(100, 24*time.Hour)

	st.Track("google", "alice@gmail.com", []string{"calendar.readonly"})
	st.Track("google", "bob@gmail.com", []string{"drive.readonly"})

	alice := st.Denials("", "alice@gmail.com")
	if len(alice) != 1 {
		t.Fatalf("expected 1 denial for alice, got %d", len(alice))
	}
}

func TestScopeTracker_Expiry(t *testing.T) {
	st := NewScopeTracker(100, 1*time.Hour)
	now := time.Now()
	st.now = func() time.Time { return now }

	st.Track("google", "user@gmail.com", []string{"calendar.readonly"})

	// Advance past expiry.
	st.now = func() time.Time { return now.Add(2 * time.Hour) }

	denials := st.Denials("", "")
	if len(denials) != 0 {
		t.Fatalf("expected 0 denials after expiry, got %d", len(denials))
	}
}

func TestScopeTracker_MaxSize(t *testing.T) {
	st := NewScopeTracker(2, 24*time.Hour)
	now := time.Now()
	st.now = func() time.Time { return now }

	st.Track("google", "user@gmail.com", []string{"scope1"})
	now = now.Add(1 * time.Second)
	st.now = func() time.Time { return now }
	st.Track("google", "user@gmail.com", []string{"scope2"})
	now = now.Add(1 * time.Second)
	st.now = func() time.Time { return now }
	st.Track("google", "user@gmail.com", []string{"scope3"}) // should evict scope1

	denials := st.Denials("", "")
	if len(denials) != 2 {
		t.Fatalf("expected 2 denials (maxSize), got %d", len(denials))
	}
	// scope1 should have been evicted (oldest).
	for _, d := range denials {
		if d.Scope == "scope1" {
			t.Error("scope1 should have been evicted")
		}
	}
}

func TestFindMissingScopes(t *testing.T) {
	tests := []struct {
		requested []string
		granted   []string
		want      []string
	}{
		{[]string{"a", "b"}, []string{"a", "b", "c"}, nil},
		{[]string{"a", "b", "d"}, []string{"a", "b", "c"}, []string{"d"}},
		{[]string{"x"}, []string{}, []string{"x"}},
		{[]string{}, []string{"a"}, nil},
	}
	for _, tt := range tests {
		got := findMissingScopes(tt.requested, tt.granted, nil)
		if len(got) != len(tt.want) {
			t.Errorf("findMissingScopes(%v, %v) = %v, want %v", tt.requested, tt.granted, got, tt.want)
		}
	}
}

func TestFindMissingScopes_Normalize(t *testing.T) {
	// Agent declares short name; credential has the full URL Google issues.
	// Without a normalizer, findMissingScopes treats them as different
	// strings and reports the scope as missing — which is the bug agents
	// kept hitting in #000005 manual testing.
	requested := []string{"gmail.readonly"}
	granted := []string{"https://www.googleapis.com/auth/gmail.readonly"}

	identity := func(s string) string { return s }
	if got := findMissingScopes(requested, granted, identity); len(got) == 0 {
		t.Errorf("identity normalize: expected mismatch, got none")
	}

	canon := func(s string) string {
		if s == "gmail.readonly" {
			return "https://www.googleapis.com/auth/gmail.readonly"
		}
		return s
	}
	if got := findMissingScopes(requested, granted, canon); len(got) != 0 {
		t.Errorf("canon normalize: got %v missing, want none", got)
	}
}

func TestHTTPProxy_ScopeEnforcement(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Header.Get("Authorization"))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	hostname := upstreamURL.Hostname()
	HostToProvider[hostname] = &Provider{Name: "test-scope", Auth: AuthBearer}
	defer delete(HostToProvider, hostname)

	store := memory.New()
	_ = store.Set(&vault.Credential{
		Provider:    "test-scope",
		Account:     "user@gmail.com",
		AccessToken: "tok",
		Scopes:      []string{"gmail.readonly", "calendar.readonly"},
	})

	tracker := NewScopeTracker(100, 24*time.Hour)
	srv := &Server{
		Vault:        store,
		Audit:        NopAuditLog(),
		CA:           testCA(t),
		ScopeTracker: tracker,
	}
	proxyServer := httptest.NewServer(srv)
	defer proxyServer.Close()

	proxyURL, _ := url.Parse(proxyServer.URL)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}

	t.Run("scope granted", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://"+upstreamURL.Host+"/test", nil)
		req.Header.Set("X-Charon-Scope", "gmail.readonly")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
	})

	t.Run("scope missing", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://"+upstreamURL.Host+"/test", nil)
		req.Header.Set("X-Charon-Scope", "drive.readonly")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 407 {
			t.Errorf("expected 407, got %d", resp.StatusCode)
		}

		var body struct {
			Error   string   `json:"error"`
			Missing []string `json:"missing"`
			Fix     string   `json:"fix"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if body.Error != "scope_missing" {
			t.Errorf("expected error 'scope_missing', got %q", body.Error)
		}
		if len(body.Missing) != 1 || body.Missing[0] != "drive.readonly" {
			t.Errorf("expected missing ['drive.readonly'], got %v", body.Missing)
		}
	})

	t.Run("no scope header passes through", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://"+upstreamURL.Host+"/test", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "Bearer tok" {
			t.Errorf("expected 'Bearer tok', got %q", body)
		}
	})

	t.Run("denial tracked", func(t *testing.T) {
		denials := tracker.Denials("test-scope", "")
		if len(denials) != 1 {
			t.Fatalf("expected 1 tracked denial, got %d", len(denials))
		}
		if denials[0].Scope != "drive.readonly" {
			t.Errorf("expected tracked scope 'drive.readonly', got %q", denials[0].Scope)
		}
	})
}

func TestHTTPProxy_MultipleRequestedScopes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	hostname := upstreamURL.Hostname()
	HostToProvider[hostname] = &Provider{Name: "test-multi", Auth: AuthBearer}
	defer delete(HostToProvider, hostname)

	store := memory.New()
	_ = store.Set(&vault.Credential{
		Provider:    "test-multi",
		Account:     "user",
		AccessToken: "tok",
		Scopes:      []string{"a"},
	})

	srv := &Server{
		Vault:        store,
		Audit:        NopAuditLog(),
		CA:           testCA(t),
		ScopeTracker: NewScopeTracker(100, 24*time.Hour),
	}
	proxyServer := httptest.NewServer(srv)
	defer proxyServer.Close()

	proxyURL, _ := url.Parse(proxyServer.URL)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}

	// Request multiple scopes, some missing.
	req, _ := http.NewRequest("GET", "http://"+upstreamURL.Host+"/test", nil)
	req.Header.Set("X-Charon-Scope", "a, b, c")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 407 {
		t.Fatalf("expected 407, got %d", resp.StatusCode)
	}
	var body struct {
		Missing []string `json:"missing"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Missing) != 2 {
		t.Errorf("expected 2 missing scopes, got %v", body.Missing)
	}
}

func TestScopeDeniedEndpoint(t *testing.T) {
	tracker := NewScopeTracker(100, 24*time.Hour)
	tracker.Track("google", "user@gmail.com", []string{"calendar.readonly"})

	srv := &Server{
		Vault:        memory.New(),
		Audit:        NopAuditLog(),
		CA:           testCA(t),
		ScopeTracker: tracker,
	}
	proxyServer := httptest.NewServer(srv)
	defer proxyServer.Close()

	resp, err := http.Get(proxyServer.URL + "/scopes/denied?provider=google")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var denials []ScopeDenial
	json.NewDecoder(resp.Body).Decode(&denials)
	if len(denials) != 1 {
		t.Fatalf("expected 1 denial, got %d", len(denials))
	}
	if denials[0].Scope != "calendar.readonly" {
		t.Errorf("expected 'calendar.readonly', got %q", denials[0].Scope)
	}
}

func TestScopeDeniedEndpoint_Empty(t *testing.T) {
	srv := &Server{
		Vault: memory.New(),
		Audit: NopAuditLog(),
		CA:    testCA(t),
		// No ScopeTracker.
	}
	proxyServer := httptest.NewServer(srv)
	defer proxyServer.Close()

	resp, err := http.Get(proxyServer.URL + "/scopes/denied")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "[]" {
		t.Errorf("expected empty array '[]', got %q", body)
	}
}
