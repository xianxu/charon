package proxy

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/xianxu/charon/internal/vault"
)

const charonAccountHeader = "X-Charon-Account"

// Server is the Charon HTTPS forward proxy.
type Server struct {
	Vault     vault.Store
	Audit     *AuditLog
	Addr      string // listen address, e.g. "127.0.0.1:8230"
	CA        *CA
	Transport http.RoundTripper
	// Now returns the current time. Defaults to time.Now. Override in tests.
	Now func() time.Time

	// tokenCache caches access tokens in memory keyed by "provider:account".
	tokenCache sync.Map
	// accountCache caches provider→account resolution for single-account providers.
	accountCache sync.Map
}

type cachedToken struct {
	token  string
	expiry time.Time
}

// ClearCache invalidates all cached tokens and account resolutions.
func (s *Server) ClearCache() {
	s.tokenCache.Range(func(k, v any) bool { s.tokenCache.Delete(k); return true })
	s.accountCache.Range(func(k, v any) bool { s.accountCache.Delete(k); return true })
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Server) transport() http.RoundTripper {
	if s.Transport != nil {
		return s.Transport
	}
	return http.DefaultTransport
}

// ListenAndServe starts the proxy server.
func (s *Server) ListenAndServe() error {
	srv := &http.Server{
		Addr:    s.Addr,
		Handler: s,
	}
	log.Printf("charon proxy listening on %s", s.Addr)
	return srv.ListenAndServe()
}

// ServeHTTP handles both CONNECT (HTTPS) and plain HTTP requests.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		s.handleConnect(w, r)
		return
	}
	// Direct requests to the proxy itself (not forwarded).
	if r.URL.Host == "" {
		s.handleDirect(w, r)
		return
	}
	s.handleHTTP(w, r)
}

// handleDirect handles requests sent directly to the proxy (health, CA cert).
func (s *Server) handleDirect(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/cache/clear":
		s.ClearCache()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"cleared":true}`)
	case "/healthz":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","addr":%q}`, s.Addr)
	case "/ca.pem":
		if s.CA == nil {
			http.Error(w, "no CA configured", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Write(s.CA.CertPEM)
	default:
		http.Error(w, "charon proxy — use HTTPS_PROXY to route traffic", http.StatusOK)
	}
}

// handleConnect handles HTTPS CONNECT tunneling with token injection.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if !strings.Contains(host, ":") {
		host += ":443"
	}
	hostname := strings.Split(host, ":")[0]

	provider := ProviderForHost(hostname)
	if provider == nil {
		s.tunnelPassthrough(w, r, host)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer clientConn.Close()

	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	tlsClientConn := tls.Server(clientConn, &tls.Config{
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := hello.ServerName
			if name == "" {
				name = hostname
			}
			return s.CA.GenerateCert(name)
		},
	})
	defer tlsClientConn.Close()
	if err := tlsClientConn.Handshake(); err != nil {
		log.Printf("TLS handshake with client failed: %v", err)
		return
	}

	clientReader := bufio.NewReader(tlsClientConn)
	for {
		req, err := http.ReadRequest(clientReader)
		if err != nil {
			if err != io.EOF {
				log.Printf("error reading request: %v", err)
			}
			return
		}

		start := s.now()
		account := req.Header.Get(charonAccountHeader)
		req.Header.Del(charonAccountHeader)

		token, resolvedAccount, err := s.resolveToken(provider.Name, account)
		entry := AuditEntry{
			Timestamp: start,
			Method:    req.Method,
			Host:      hostname,
			Path:      req.URL.Path,
			Provider:  provider.Name,
			Account:   resolvedAccount,
		}

		if err != nil {
			log.Printf("credential error for %s/%s: %v", provider.Name, account, err)
			entry.Error = err.Error()
			s.Audit.Log(entry)
			resp := &http.Response{
				StatusCode: http.StatusProxyAuthRequired,
				Status:     "407 Proxy Authentication Required",
				Proto:      "HTTP/1.1",
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header:     http.Header{"Content-Type": {"text/plain"}},
				Body:       io.NopCloser(strings.NewReader("charon: credential required\n")),
			}
			_ = resp.Write(tlsClientConn)
			return
		}

		if err := provider.InjectAuth(req.Header.Set, token); err != nil {
			entry.Error = err.Error()
			s.Audit.Log(entry)
			return
		}

		req.URL.Scheme = "https"
		req.URL.Host = host
		req.RequestURI = ""

		resp, err := s.transport().RoundTrip(req)
		if err != nil {
			entry.Error = err.Error()
			entry.LatencyMs = time.Since(start).Milliseconds()
			s.Audit.Log(entry)
			log.Printf("upstream error: %v", err)
			return
		}

		entry.StatusCode = resp.StatusCode
		entry.LatencyMs = time.Since(start).Milliseconds()
		s.Audit.Log(entry)

		// Stream response: write status/headers, then copy body.
		fmt.Fprintf(tlsClientConn, "HTTP/%d.%d %d %s\r\n", resp.ProtoMajor, resp.ProtoMinor, resp.StatusCode, resp.Status)
		_ = resp.Header.Write(tlsClientConn)
		_, _ = fmt.Fprintf(tlsClientConn, "\r\n")
		_, _ = io.Copy(tlsClientConn, resp.Body)
		_ = resp.Body.Close()
	}
}

// tunnelPassthrough creates a raw TCP tunnel for unknown hosts.
func (s *Server) tunnelPassthrough(w http.ResponseWriter, r *http.Request, host string) {
	upstream, err := net.DialTimeout("tcp", host, 10*time.Second)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		upstream.Close()
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		upstream.Close()
		return
	}

	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(upstream, clientConn)
		close(done)
	}()
	_, _ = io.Copy(clientConn, upstream)
	clientConn.Close()
	<-done
	upstream.Close()
}

// handleHTTP handles plain HTTP requests (non-CONNECT).
func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	start := s.now()
	hostname := r.URL.Hostname()
	provider := ProviderForHost(hostname)

	entry := AuditEntry{
		Timestamp: start,
		Method:    r.Method,
		Host:      hostname,
		Path:      r.URL.Path,
	}

	if provider != nil {
		entry.Provider = provider.Name
		account := r.Header.Get(charonAccountHeader)
		r.Header.Del(charonAccountHeader)

		token, resolvedAccount, err := s.resolveToken(provider.Name, account)
		entry.Account = resolvedAccount
		if err != nil {
			entry.Error = err.Error()
			s.Audit.Log(entry)
			http.Error(w, "charon: credential required", http.StatusProxyAuthRequired)
			return
		}
		if err := provider.InjectAuth(r.Header.Set, token); err != nil {
			entry.Error = err.Error()
			s.Audit.Log(entry)
			http.Error(w, "charon: unsupported auth method", http.StatusInternalServerError)
			return
		}
	}

	r.RequestURI = ""
	resp, err := s.transport().RoundTrip(r)
	if err != nil {
		entry.Error = err.Error()
		s.Audit.Log(entry)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	entry.StatusCode = resp.StatusCode
	entry.LatencyMs = time.Since(start).Milliseconds()
	s.Audit.Log(entry)

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// resolveToken gets an access token for the given provider/account.
func (s *Server) resolveToken(providerName, account string) (token, resolvedAccount string, err error) {
	if account == "" {
		// Check account cache first to avoid calling security dump-keychain.
		if cached, ok := s.accountCache.Load(providerName); ok {
			account = cached.(string)
		} else {
			creds, err := s.Vault.List()
			if err != nil {
				return "", "", fmt.Errorf("failed to list credentials: %w", err)
			}
			var matches []*vault.Credential
			for _, c := range creds {
				if c.Provider == providerName {
					matches = append(matches, c)
				}
			}
			switch len(matches) {
			case 0:
				return "", "", fmt.Errorf("no credentials for provider %q", providerName)
			case 1:
				account = matches[0].Account
				s.accountCache.Store(providerName, account)
			default:
				return "", "", fmt.Errorf("multiple accounts for provider %q, set %s header", providerName, charonAccountHeader)
			}
		}
	}

	cacheKey := providerName + ":" + account
	now := s.now()
	if cached, ok := s.tokenCache.Load(cacheKey); ok {
		ct := cached.(*cachedToken)
		if ct.expiry.IsZero() || now.Before(ct.expiry.Add(-vault.GracePeriod)) {
			return ct.token, account, nil
		}
	}

	cred, err := s.Vault.Get(providerName, account)
	if err != nil {
		return "", account, err
	}

	if cred.AccessToken != "" && !cred.IsExpiredAt(now) {
		s.tokenCache.Store(cacheKey, &cachedToken{
			token:  cred.AccessToken,
			expiry: cred.Expiry,
		})
		return cred.AccessToken, account, nil
	}

	if cred.AccessToken != "" {
		return cred.AccessToken, account, nil
	}

	return "", account, fmt.Errorf("no access token for %s/%s (refresh not yet implemented)", providerName, account)
}
