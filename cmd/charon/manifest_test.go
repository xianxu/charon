package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
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
		`"https://www.googleapis.com/auth/gmail.readonly"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("manifest JSON missing %q\n%s", want, s)
		}
	}
}
