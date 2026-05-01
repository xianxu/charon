package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/xianxu/charon/internal/vault"
	"github.com/xianxu/charon/internal/vault/memory"
)

// proxySection extracts the proxy block as map[string]any. Manifest
// is heterogeneous (string fields + a bool `running`), so we can't
// type-assert to map[string]string anymore.
func proxySection(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	p, ok := m["proxy"].(map[string]any)
	if !ok {
		t.Fatalf("proxy section wrong type: %T", m["proxy"])
	}
	return p
}

func TestManifestPayload_ProxySectionUsesAddr(t *testing.T) {
	got, err := manifestPayload(fixtureVault(t), "127.0.0.1:8230")
	if err != nil {
		t.Fatalf("manifestPayload: %v", err)
	}
	proxy := proxySection(t, got)
	if proxy["addr"] != "127.0.0.1:8230" {
		t.Errorf("addr = %v", proxy["addr"])
	}
	if proxy["url"] != "http://127.0.0.1:8230" {
		t.Errorf("url = %v", proxy["url"])
	}
	if proxy["ca_pem_url"] != "http://127.0.0.1:8230/ca.pem" {
		t.Errorf("ca_pem_url = %v", proxy["ca_pem_url"])
	}
	if _, ok := proxy["running"].(bool); !ok {
		t.Errorf("expected running to be bool, got %T", proxy["running"])
	}
}

func TestManifestPayload_HonorsCustomAddr(t *testing.T) {
	got, err := manifestPayload(fixtureVault(t), "0.0.0.0:9999")
	if err != nil {
		t.Fatalf("manifestPayload: %v", err)
	}
	proxy := proxySection(t, got)
	if proxy["url"] != "http://0.0.0.0:9999" {
		t.Errorf("proxy.url = %v, want http://0.0.0.0:9999", proxy["url"])
	}
	if proxy["ca_pem_url"] != "http://0.0.0.0:9999/ca.pem" {
		t.Errorf("proxy.ca_pem_url = %v, want http://0.0.0.0:9999/ca.pem", proxy["ca_pem_url"])
	}
}

// Probing a port nothing is listening on must yield running=false
// rather than an error. The manifest is best-effort agent-facing
// info; "down" is the actionable signal.
func TestManifestPayload_RunningFalseWhenProxyDown(t *testing.T) {
	// 127.0.0.1:1 is reserved (tcpmux) and almost never bound on a
	// developer machine; if it ever is, the test would falsely pass
	// with running=true. Acceptable risk for a unit test.
	got, err := manifestPayload(fixtureVault(t), "127.0.0.1:1")
	if err != nil {
		t.Fatalf("manifestPayload: %v", err)
	}
	proxy := proxySection(t, got)
	if proxy["running"] != false {
		t.Errorf("expected running=false for unreachable addr, got %v", proxy["running"])
	}
}

// Manifest's permissions section must equal what permissionsPayload returns —
// the manifest is a wrapper, not a re-implementation.
func TestManifestPayload_PermissionsMatchesHelper(t *testing.T) {
	v := fixtureVault(t)
	manifest, err := manifestPayload(v, "127.0.0.1:8230")
	if err != nil {
		t.Fatalf("manifestPayload: %v", err)
	}
	perms, err := permissionsPayload(v)
	if err != nil {
		t.Fatalf("permissionsPayload: %v", err)
	}
	if !reflect.DeepEqual(manifest["permissions"], perms) {
		t.Errorf("manifest.permissions != permissionsPayload\n manifest=%v\n helper=%v",
			manifest["permissions"], perms)
	}
}

func TestManifestPayload_RoundTripsThroughJSON(t *testing.T) {
	got, err := manifestPayload(fixtureVault(t), "127.0.0.1:8230")
	if err != nil {
		t.Fatalf("manifestPayload: %v", err)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	s := string(b)
	for _, want := range []string{
		`"addr":"127.0.0.1:8230"`,
		`"url":"http://127.0.0.1:8230"`,
		`"ca_pem_url":"http://127.0.0.1:8230/ca.pem"`,
		`"alice@gmail.com"`,
		`"scopes":[`,
		`"https://www.googleapis.com/auth/gmail.readonly"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("manifest JSON missing %q\n%s", want, s)
		}
	}
}

// JSON shape with vertex set: agent reads "scopes" and "vertex"
// siblings under each account; vertex is omitted when absent.
func TestManifestPayload_VertexInJSON(t *testing.T) {
	v := memory.New()
	v.Set(&vault.Credential{
		Provider: "google",
		Account:  "alice@gmail.com",
		Scopes:   []string{"https://www.googleapis.com/auth/cloud-platform"},
		GCP: &vault.GCPData{
			ProjectID:    "alice-charon",
			VertexRegion: "us-central1",
		},
	})
	v.Set(&vault.Credential{
		Provider: "google",
		Account:  "no-gcp@gmail.com",
		Scopes:   []string{"openid"},
	})
	got, err := manifestPayload(v, "127.0.0.1:8230")
	if err != nil {
		t.Fatalf("manifestPayload: %v", err)
	}
	b, _ := json.Marshal(got)
	s := string(b)

	for _, want := range []string{
		`"vertex":{"project_id":"alice-charon","region":"us-central1"}`,
		`"scopes":["https://www.googleapis.com/auth/cloud-platform"]`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("alice manifest fragment missing %q\n%s", want, s)
		}
	}
	// no-gcp account must omit the vertex key entirely (omitempty).
	if !strings.Contains(s, `"no-gcp@gmail.com":{"scopes":["openid"]}`) {
		t.Errorf("no-gcp account should not include vertex key:\n%s", s)
	}
}
