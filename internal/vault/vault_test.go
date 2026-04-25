package vault

import (
	"testing"
	"time"
)

func TestIsExpired_EmptyToken(t *testing.T) {
	c := &Credential{AccessToken: ""}
	if !c.IsExpired() {
		t.Error("empty access token should be expired")
	}
}

func TestIsExpired_ZeroExpiry(t *testing.T) {
	c := &Credential{AccessToken: "tok"}
	if c.IsExpired() {
		t.Error("zero expiry should mean never expires")
	}
}

func TestIsExpired_FutureExpiry(t *testing.T) {
	c := &Credential{
		AccessToken: "tok",
		Expiry:      time.Now().Add(10 * time.Minute),
	}
	if c.IsExpired() {
		t.Error("token with future expiry should not be expired")
	}
}

func TestIsExpired_PastExpiry(t *testing.T) {
	c := &Credential{
		AccessToken: "tok",
		Expiry:      time.Now().Add(-1 * time.Minute),
	}
	if !c.IsExpired() {
		t.Error("token with past expiry should be expired")
	}
}

func TestIsExpired_WithinGracePeriod(t *testing.T) {
	// Token expires in 20 seconds — within the 30-second grace period.
	c := &Credential{
		AccessToken: "tok",
		Expiry:      time.Now().Add(20 * time.Second),
	}
	if !c.IsExpired() {
		t.Error("token within 30s grace period should be considered expired")
	}
}
