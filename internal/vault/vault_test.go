package vault

import (
	"testing"
	"time"
)

var baseTime = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func TestIsExpiredAt_EmptyToken(t *testing.T) {
	c := &Credential{AccessToken: ""}
	if !c.IsExpiredAt(baseTime) {
		t.Error("empty access token should be expired")
	}
}

func TestIsExpiredAt_ZeroExpiry(t *testing.T) {
	c := &Credential{AccessToken: "tok"}
	if c.IsExpiredAt(baseTime) {
		t.Error("zero expiry should mean never expires")
	}
	// Still not expired 100 years later.
	if c.IsExpiredAt(baseTime.Add(100 * 365 * 24 * time.Hour)) {
		t.Error("zero expiry should mean never expires, even far in the future")
	}
}

func TestIsExpiredAt_FutureExpiry(t *testing.T) {
	c := &Credential{
		AccessToken: "tok",
		Expiry:      baseTime.Add(10 * time.Minute),
	}
	if c.IsExpiredAt(baseTime) {
		t.Error("token with 10min remaining should not be expired")
	}
}

func TestIsExpiredAt_PastExpiry(t *testing.T) {
	c := &Credential{
		AccessToken: "tok",
		Expiry:      baseTime.Add(-1 * time.Minute),
	}
	if !c.IsExpiredAt(baseTime) {
		t.Error("token expired 1min ago should be expired")
	}
}

func TestIsExpiredAt_ExactlyAtExpiry(t *testing.T) {
	c := &Credential{
		AccessToken: "tok",
		Expiry:      baseTime,
	}
	// At expiry time, the token is within the grace period, so expired.
	if !c.IsExpiredAt(baseTime) {
		t.Error("token at exact expiry should be expired (within grace period)")
	}
}

func TestIsExpiredAt_WithinGracePeriod(t *testing.T) {
	c := &Credential{
		AccessToken: "tok",
		Expiry:      baseTime.Add(20 * time.Second),
	}
	// 20s remaining < 30s grace → expired.
	if !c.IsExpiredAt(baseTime) {
		t.Error("token within 30s grace period should be expired")
	}
}

func TestIsExpiredAt_JustOutsideGracePeriod(t *testing.T) {
	c := &Credential{
		AccessToken: "tok",
		Expiry:      baseTime.Add(31 * time.Second),
	}
	// 31s remaining > 30s grace → not expired.
	if c.IsExpiredAt(baseTime) {
		t.Error("token with 31s remaining should not be expired")
	}
}

func TestIsExpiredAt_TimePasses(t *testing.T) {
	c := &Credential{
		AccessToken: "tok",
		Expiry:      baseTime.Add(5 * time.Minute),
	}

	// t=0: 5min remaining → valid
	if c.IsExpiredAt(baseTime) {
		t.Error("t=0: should be valid")
	}

	// t=+4m: 1min remaining → valid (outside grace)
	if c.IsExpiredAt(baseTime.Add(4 * time.Minute)) {
		t.Error("t=+4m: should be valid")
	}

	// t=+4m31s: 29s remaining → expired (within grace)
	if !c.IsExpiredAt(baseTime.Add(4*time.Minute + 31*time.Second)) {
		t.Error("t=+4m31s: should be expired (within grace)")
	}

	// t=+5m: 0s remaining → expired
	if !c.IsExpiredAt(baseTime.Add(5 * time.Minute)) {
		t.Error("t=+5m: should be expired")
	}

	// t=+6m: -1min → definitely expired
	if !c.IsExpiredAt(baseTime.Add(6 * time.Minute)) {
		t.Error("t=+6m: should be expired")
	}
}
