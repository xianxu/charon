package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/xianxu/charon/internal/vault"
)

const (
	obKey          = "charon-credential-proxy-obfuscation"
	obClientID     = "5a51594157591a504a55575643150c0f1a481a1b5c4a4e431d05121d4007111c0a59065250061f1c041a5a41014a041e041a4f0b421f15031d0c5e0a10051a1d17041a1d410d0c05"
	obClientSecret = "242722213f360028202410033d440c145f6821021755007837072210322757232d000d"

	googleAuthURL  = "https://accounts.google.com/o/oauth2/auth"
	googleTokenURL = "https://oauth2.googleapis.com/token"
)

// DefaultGoogleScopes are requested if none specified.
var DefaultGoogleScopes = []string{
	"https://www.googleapis.com/auth/gmail.readonly",
}

// GoogleProvider implements the OAuth flow for Google accounts.
type GoogleProvider struct {
	clientID     string
	clientSecret string
}

func NewGoogleProvider() (*GoogleProvider, error) {
	cid, err := XORDecode(obClientID, obKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode client_id: %w", err)
	}
	cs, err := XORDecode(obClientSecret, obKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode client_secret: %w", err)
	}
	return &GoogleProvider{clientID: cid, clientSecret: cs}, nil
}

// tokenResponse is the JSON response from Google's token endpoint.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// Auth runs the OAuth authorization flow: opens browser, waits for callback, exchanges code for tokens.
func (g *GoogleProvider) Auth(account string, scopes []string, existingScopes []string) (*vault.Credential, error) {
	if len(scopes) == 0 {
		scopes = DefaultGoogleScopes
	}
	// Merge with existing scopes for incremental authorization.
	allScopes := mergeScopes(scopes, existingScopes)

	// Start local callback server.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to start callback server: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d", port)

	// Build authorization URL.
	authURL := g.buildAuthURL(redirectURI, allScopes)

	// Open browser.
	fmt.Printf("Opening browser for Google OAuth...\n")
	fmt.Printf("If browser doesn't open, visit:\n%s\n\n", authURL)
	openBrowser(authURL)

	// Wait for callback with authorization code.
	code, err := waitForCallback(ln)
	if err != nil {
		return nil, fmt.Errorf("OAuth callback failed: %w", err)
	}

	// Exchange code for tokens.
	return g.exchangeCode(code, redirectURI, account)
}

// Refresh uses a refresh token to get a new access token.
func (g *GoogleProvider) Refresh(cred *vault.Credential) (*vault.Credential, error) {
	if cred.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token for %s/%s", cred.Provider, cred.Account)
	}

	data := url.Values{
		"client_id":     {g.clientID},
		"client_secret": {g.clientSecret},
		"refresh_token": {cred.RefreshToken},
		"grant_type":    {"refresh_token"},
	}

	resp, err := http.PostForm(googleTokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("token refresh failed: %w", err)
	}
	defer resp.Body.Close()

	var tok tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}
	if tok.Error != "" {
		return nil, fmt.Errorf("token refresh error: %s: %s", tok.Error, tok.ErrorDesc)
	}

	updated := &vault.Credential{
		Provider:     cred.Provider,
		Account:      cred.Account,
		AccessToken:  tok.AccessToken,
		RefreshToken: cred.RefreshToken,
		Expiry:       time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second),
		Scopes:       cred.Scopes,
	}

	// Handle refresh token rotation — Google may return a new refresh token.
	if tok.RefreshToken != "" {
		updated.RefreshToken = tok.RefreshToken
	}

	// Update scopes if changed.
	if tok.Scope != "" {
		updated.Scopes = strings.Split(tok.Scope, " ")
	}

	return updated, nil
}

func (g *GoogleProvider) buildAuthURL(redirectURI string, scopes []string) string {
	params := url.Values{
		"client_id":             {g.clientID},
		"redirect_uri":         {redirectURI},
		"response_type":        {"code"},
		"scope":                {strings.Join(scopes, " ")},
		"access_type":          {"offline"}, // request refresh token
		"prompt":               {"consent"}, // force consent to get refresh token
		"include_granted_scopes": {"true"},  // incremental authorization
	}
	return googleAuthURL + "?" + params.Encode()
}

func (g *GoogleProvider) exchangeCode(code, redirectURI, account string) (*vault.Credential, error) {
	data := url.Values{
		"client_id":     {g.clientID},
		"client_secret": {g.clientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"grant_type":    {"authorization_code"},
	}

	resp, err := http.PostForm(googleTokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}
	defer resp.Body.Close()

	var tok tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}
	if tok.Error != "" {
		return nil, fmt.Errorf("token exchange error: %s: %s", tok.Error, tok.ErrorDesc)
	}

	var scopes []string
	if tok.Scope != "" {
		scopes = strings.Split(tok.Scope, " ")
	}

	return &vault.Credential{
		Provider:     "google",
		Account:      account,
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		Expiry:       time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second),
		Scopes:       scopes,
	}, nil
}

// waitForCallback starts an HTTP server, waits for the OAuth callback, extracts the code.
func waitForCallback(ln net.Listener) (string, error) {
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			code := r.URL.Query().Get("code")
			if code == "" {
				errMsg := r.URL.Query().Get("error")
				if errMsg == "" {
					errMsg = "no authorization code received"
				}
				fmt.Fprintf(w, "<html><body><h1>Authorization Failed</h1><p>%s</p><p>You can close this tab.</p></body></html>", errMsg)
				errCh <- fmt.Errorf("OAuth error: %s", errMsg)
				return
			}
			fmt.Fprint(w, "<html><body><h1>Authorization Successful</h1><p>You can close this tab and return to the terminal.</p></body></html>")
			codeCh <- code
		}),
	}

	go srv.Serve(ln)

	select {
	case code := <-codeCh:
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		return code, nil
	case err := <-errCh:
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		return "", err
	case <-time.After(5 * time.Minute):
		srv.Close()
		return "", fmt.Errorf("OAuth callback timed out (5 minutes)")
	}
}

func mergeScopes(requested, existing []string) []string {
	seen := make(map[string]bool)
	for _, s := range existing {
		seen[s] = true
	}
	for _, s := range requested {
		seen[s] = true
	}
	var merged []string
	for s := range seen {
		if s != "" {
			merged = append(merged, s)
		}
	}
	return merged
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		log.Printf("Please open this URL manually: %s", url)
		return
	}
	_ = cmd.Start()
}
