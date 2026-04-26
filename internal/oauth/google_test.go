package oauth

import (
	"encoding/base64"
	"testing"
)

func TestParseIDTokenEmail(t *testing.T) {
	tests := []struct {
		name    string
		token   string
		want    string
		wantErr bool
	}{
		{
			name:  "valid token",
			token: makeTestJWT(`{"email":"test@gmail.com","sub":"123"}`),
			want:  "test@gmail.com",
		},
		{
			name:    "empty token",
			token:   "",
			wantErr: true,
		},
		{
			name:    "invalid format",
			token:   "not-a-jwt",
			wantErr: true,
		},
		{
			name:    "missing email claim",
			token:   makeTestJWT(`{"sub":"123"}`),
			wantErr: true,
		},
		{
			name:    "empty email claim",
			token:   makeTestJWT(`{"email":"","sub":"123"}`),
			wantErr: true,
		},
		{
			name:    "invalid base64 payload",
			token:   "header.!!!invalid!!!.signature",
			wantErr: true,
		},
		{
			name:    "invalid json payload",
			token:   "header." + base64.RawURLEncoding.EncodeToString([]byte("not json")) + ".sig",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseIDTokenEmail(tt.token)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseIDTokenEmail() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseIDTokenEmail() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildAuthURL_LoginHint(t *testing.T) {
	gp := &GoogleProvider{clientID: "test-client-id", clientSecret: "test-secret"}

	t.Run("without login hint", func(t *testing.T) {
		u := gp.buildAuthURL("http://localhost:1234", []string{"openid"}, "")
		if containsParam(u, "login_hint") {
			t.Error("expected no login_hint parameter")
		}
	})

	t.Run("with login hint", func(t *testing.T) {
		u := gp.buildAuthURL("http://localhost:1234", []string{"openid"}, "user@gmail.com")
		if !containsParam(u, "login_hint") {
			t.Error("expected login_hint parameter")
		}
	})
}

func TestMergeScopes(t *testing.T) {
	merged := mergeScopes([]string{"a", "b"}, []string{"b", "c"})
	seen := make(map[string]bool)
	for _, s := range merged {
		seen[s] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !seen[want] {
			t.Errorf("missing scope %q in merged result %v", want, merged)
		}
	}
	if len(merged) != 3 {
		t.Errorf("expected 3 scopes, got %d: %v", len(merged), merged)
	}
}

func TestRequiredScopesIncluded(t *testing.T) {
	// Verify that requiredGoogleScopes include openid and email.
	seen := make(map[string]bool)
	for _, s := range requiredGoogleScopes {
		seen[s] = true
	}
	if !seen["openid"] {
		t.Error("requiredGoogleScopes missing 'openid'")
	}
	if !seen["https://www.googleapis.com/auth/userinfo.email"] {
		t.Error("requiredGoogleScopes missing userinfo.email")
	}
}

// makeTestJWT creates a minimal JWT with the given JSON payload.
func makeTestJWT(payload string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	body := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return header + "." + body + ".fake-signature"
}

// containsParam checks if a URL string contains a query parameter.
func containsParam(urlStr, param string) bool {
	return len(urlStr) > 0 && len(param) > 0 &&
		(contains(urlStr, param+"=") || contains(urlStr, param+"%3D"))
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
