package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/xianxu/charon/internal/vault"
	"github.com/xianxu/charon/internal/vault/memory"
)

func TestManifestPayload_ProxySectionUsesAddr(t *testing.T) {
	got, err := manifestPayload(fixtureVault(t), "127.0.0.1:8230")
	if err != nil {
		t.Fatalf("manifestPayload: %v", err)
	}
	proxy, ok := got["proxy"].(map[string]string)
	if !ok {
		t.Fatalf("proxy section wrong type: %T", got["proxy"])
	}
	want := map[string]string{
		"addr":       "127.0.0.1:8230",
		"url":        "http://127.0.0.1:8230",
		"ca_pem_url": "http://127.0.0.1:8230/ca.pem",
	}
	if !reflect.DeepEqual(proxy, want) {
		t.Errorf("proxy section mismatch.\n got=%v\nwant=%v", proxy, want)
	}
}

func TestManifestPayload_HonorsCustomAddr(t *testing.T) {
	got, err := manifestPayload(fixtureVault(t), "0.0.0.0:9999")
	if err != nil {
		t.Fatalf("manifestPayload: %v", err)
	}
	proxy := got["proxy"].(map[string]string)
	if proxy["url"] != "http://0.0.0.0:9999" {
		t.Errorf("proxy.url = %q, want http://0.0.0.0:9999", proxy["url"])
	}
	if proxy["ca_pem_url"] != "http://0.0.0.0:9999/ca.pem" {
		t.Errorf("proxy.ca_pem_url = %q, want http://0.0.0.0:9999/ca.pem", proxy["ca_pem_url"])
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

// JSON shape with GCP set: agent reads "scopes" and "gcp" siblings
// under each account; gcp is omitted when absent (omitempty).
func TestManifestPayload_GCPInJSON(t *testing.T) {
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
		`"alice@gmail.com":{"scopes":["https://www.googleapis.com/auth/cloud-platform"],"gcp":{`,
		`"project_id":"alice-charon"`,
		`"vertex_region":"us-central1"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("alice manifest fragment missing %q\n%s", want, s)
		}
	}
	// no-gcp account must omit the gcp key entirely (omitempty).
	if !strings.Contains(s, `"no-gcp@gmail.com":{"scopes":["openid"]}`) {
		t.Errorf("no-gcp account should not include gcp key:\n%s", s)
	}
}
