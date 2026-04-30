package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/xianxu/charon/internal/vault"
	"github.com/xianxu/charon/internal/vault/memory"
)

func testCA(t *testing.T) *CA {
	t.Helper()
	ca, err := generateCA()
	if err != nil {
		t.Fatalf("failed to generate test CA: %v", err)
	}
	return ca
}

func TestHTTPProxyInjectsToken(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Header.Get("Authorization"))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	host := upstreamURL.Hostname() + ":" + upstreamURL.Port()
	hostname := upstreamURL.Hostname()
	HostToProvider[hostname] = &Provider{Name: "test-provider", Auth: AuthBearer}
	defer delete(HostToProvider, hostname)

	store := memory.New()
	_ = store.Set(&vault.Credential{
		Provider:    "test-provider",
		Account:     "testuser",
		AccessToken: "secret-token-123",
	})

	srv := &Server{
		Vault: store,
		Audit: NopAuditLog(),
		CA:    testCA(t),
	}
	proxyServer := httptest.NewServer(srv)
	defer proxyServer.Close()

	proxyURL, _ := url.Parse(proxyServer.URL)
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}

	resp, err := client.Get("http://" + host + "/test")
	if err != nil {
		t.Fatalf("request through proxy failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "Bearer secret-token-123" {
		t.Errorf("expected 'Bearer secret-token-123', got %q", body)
	}
}

func TestHTTPProxyPassthroughUnknownHost(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello")
	}))
	defer upstream.Close()

	srv := &Server{
		Vault: memory.New(),
		Audit: NopAuditLog(),
		CA:    testCA(t),
	}
	proxyServer := httptest.NewServer(srv)
	defer proxyServer.Close()

	proxyURL, _ := url.Parse(proxyServer.URL)
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}

	resp, err := client.Get(upstream.URL + "/test")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello" {
		t.Errorf("expected 'hello', got %q", body)
	}
}

func TestHTTPProxyMultiAccountRequiresHeader(t *testing.T) {
	store := memory.New()
	_ = store.Set(&vault.Credential{Provider: "google", Account: "a@gmail.com", AccessToken: "tok-a"})
	_ = store.Set(&vault.Credential{Provider: "google", Account: "b@gmail.com", AccessToken: "tok-b"})

	srv := &Server{
		Vault: store,
		Audit: NopAuditLog(),
		CA:    testCA(t),
	}
	proxyServer := httptest.NewServer(srv)
	defer proxyServer.Close()

	proxyURL, _ := url.Parse(proxyServer.URL)
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}

	resp, err := client.Get("http://gmail.googleapis.com/test")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Errorf("expected 407, got %d", resp.StatusCode)
	}
}

// Admin-key (post-#13) credentials: KeyMaterial is the token; no
// refresh, no scopes. The proxy reads it directly out of the
// AdminKey payload and injects as Authorization: Bearer.
func TestHTTPProxy_AdminKeyCredential_InjectsKeyMaterial(t *testing.T) {
	var seenAuth, seenCharonHeaders string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		// Internal headers must NOT leak to upstream.
		seenCharonHeaders = r.Header.Get("X-Charon-Account") + "|" + r.Header.Get("X-Charon-Scope")
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	hostname := upstreamURL.Hostname()
	HostToProvider[hostname] = &Provider{Name: "openai-test", Auth: AuthBearer, HasScopes: false}
	defer delete(HostToProvider, hostname)

	store := memory.New()
	_ = store.Set(&vault.Credential{
		Type:     vault.TypeAdminKey,
		Provider: "openai-test",
		Account:  "image-gen",
		AdminKey: &vault.AdminKeyData{
			OrgID:       "org-test-001",
			ProjectID:   "proj_X",
			KeyID:       "svc_acct_X",
			KeyMaterial: "sk-test-secret-bytes",
		},
	})

	srv := &Server{Vault: store, Audit: NopAuditLog(), CA: testCA(t)}
	proxyServer := httptest.NewServer(srv)
	defer proxyServer.Close()

	proxyURL, _ := url.Parse(proxyServer.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	req, _ := http.NewRequest("GET", "http://"+upstreamURL.Host+"/v1/images/generations", nil)
	req.Header.Set("X-Charon-Account", "image-gen")
	// X-Charon-Scope on an admin-key route should be silently ignored
	// (not 407) per the agent-protocol contract.
	req.Header.Set("X-Charon-Scope", "openid")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (X-Charon-Scope should be ignored on admin-key routes)", resp.StatusCode)
	}
	if seenAuth != "Bearer sk-test-secret-bytes" {
		t.Errorf("upstream Authorization = %q, want 'Bearer sk-test-secret-bytes'", seenAuth)
	}
	if seenCharonHeaders != "|" {
		t.Errorf("X-Charon-* headers leaked to upstream: %q", seenCharonHeaders)
	}
}

// resolveToken on an admin-key credential whose AdminKey payload is
// missing/empty surfaces a clear error rather than silently injecting
// an empty Bearer token.
func TestHTTPProxy_AdminKeyMissingMaterial_FailsClosed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "should not reach upstream")
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	hostname := upstreamURL.Hostname()
	HostToProvider[hostname] = &Provider{Name: "openai-test", Auth: AuthBearer, HasScopes: false}
	defer delete(HostToProvider, hostname)

	store := memory.New()
	_ = store.Set(&vault.Credential{
		Type:     vault.TypeAdminKey,
		Provider: "openai-test",
		Account:  "broken",
		AdminKey: &vault.AdminKeyData{ProjectID: "proj_X", KeyID: "svc_X"}, // KeyMaterial empty
	})

	srv := &Server{Vault: store, Audit: NopAuditLog(), CA: testCA(t)}
	proxyServer := httptest.NewServer(srv)
	defer proxyServer.Close()

	proxyURL, _ := url.Parse(proxyServer.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	req, _ := http.NewRequest("GET", "http://"+upstreamURL.Host+"/v1/test", nil)
	req.Header.Set("X-Charon-Account", "broken")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Errorf("expected 407 on empty key material, got %d", resp.StatusCode)
	}
}

func TestHTTPProxyAccountSelection(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Header.Get("Authorization"))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	hostname := upstreamURL.Hostname()
	HostToProvider[hostname] = &Provider{Name: "multi", Auth: AuthBearer}
	defer delete(HostToProvider, hostname)

	store := memory.New()
	_ = store.Set(&vault.Credential{Provider: "multi", Account: "alice", AccessToken: "tok-alice"})
	_ = store.Set(&vault.Credential{Provider: "multi", Account: "bob", AccessToken: "tok-bob"})

	srv := &Server{
		Vault: store,
		Audit: NopAuditLog(),
		CA:    testCA(t),
	}
	proxyServer := httptest.NewServer(srv)
	defer proxyServer.Close()

	proxyURL, _ := url.Parse(proxyServer.URL)
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}

	req, _ := http.NewRequest("GET", "http://"+upstreamURL.Host+"/test", nil)
	req.Header.Set("X-Charon-Account", "bob")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "Bearer tok-bob" {
		t.Errorf("expected 'Bearer tok-bob', got %q", body)
	}
}

func TestCONNECTPassthroughUnknownHost(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "secure-hello")
	}))
	defer upstream.Close()

	srv := &Server{
		Vault: memory.New(),
		Audit: NopAuditLog(),
		CA:    testCA(t),
	}
	proxyListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxyListener.Close()

	go http.Serve(proxyListener, srv)

	proxyURL, _ := url.Parse("http://" + proxyListener.Addr().String())
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	resp, err := client.Get(upstream.URL + "/test")
	if err != nil {
		t.Fatalf("CONNECT tunnel failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "secure-hello" {
		t.Errorf("expected 'secure-hello', got %q", body)
	}
}

func TestCONNECTInterceptionInjectsToken(t *testing.T) {
	// Upstream HTTPS server that echoes the Authorization header.
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Header.Get("Authorization"))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	hostname := upstreamURL.Hostname()
	HostToProvider[hostname] = &Provider{Name: "test-provider", Auth: AuthBearer}
	defer delete(HostToProvider, hostname)

	store := memory.New()
	_ = store.Set(&vault.Credential{
		Provider:    "test-provider",
		Account:     "testuser",
		AccessToken: "intercepted-token",
	})

	ca := testCA(t)
	srv := &Server{
		Vault: store,
		Audit: NopAuditLog(),
		CA:    ca,
		// Custom transport that trusts the upstream test server.
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	proxyListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxyListener.Close()
	go http.Serve(proxyListener, srv)

	// Client trusts the proxy's CA.
	caPool := x509.NewCertPool()
	caPool.AddCert(ca.Cert)

	proxyURL, _ := url.Parse("http://" + proxyListener.Addr().String())
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{
				RootCAs: caPool,
			},
		},
	}

	resp, err := client.Get("https://" + upstreamURL.Host + "/test")
	if err != nil {
		t.Fatalf("CONNECT interception failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "Bearer intercepted-token" {
		t.Errorf("expected 'Bearer intercepted-token', got %q", body)
	}
}

func TestHealthEndpoint(t *testing.T) {
	srv := &Server{
		Vault: memory.New(),
		Audit: NopAuditLog(),
		Addr:  "127.0.0.1:8230",
		CA:    testCA(t),
	}
	proxyServer := httptest.NewServer(srv)
	defer proxyServer.Close()

	resp, err := http.Get(proxyServer.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if got := string(body); got == "" {
		t.Error("expected non-empty health response")
	}
}

func TestCAEndpoint(t *testing.T) {
	ca := testCA(t)
	srv := &Server{
		Vault: memory.New(),
		Audit: NopAuditLog(),
		CA:    ca,
	}
	proxyServer := httptest.NewServer(srv)
	defer proxyServer.Close()

	resp, err := http.Get(proxyServer.URL + "/ca.pem")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != string(ca.CertPEM) {
		t.Error("CA endpoint did not return the CA cert")
	}
}

func TestRoutingTable(t *testing.T) {
	tests := []struct {
		host     string
		provider string
	}{
		{"gmail.googleapis.com", "google"},
		{"www.googleapis.com", "google"},
		{"drive.googleapis.com", "google"},
		{"unknown.example.com", ""},
	}
	for _, tt := range tests {
		p := ProviderForHost(tt.host)
		got := ""
		if p != nil {
			got = p.Name
		}
		if got != tt.provider {
			t.Errorf("ProviderForHost(%q) = %q, want %q", tt.host, got, tt.provider)
		}
	}
}
