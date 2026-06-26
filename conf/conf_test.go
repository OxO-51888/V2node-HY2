package conf

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadUnlockConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := []byte(`{
  "Log": {"Level": "info", "Output": "", "Access": "none"},
  "Nodes": [
    {"ApiHost": "https://example.com", "NodeID": 1, "ApiKey": "test"}
  ],
  "Unlock": {
    "Enable": true,
    "DefaultOutbound": "sg",
    "SOCKS": [
      {"Tag": "sg", "Address": "193.25.215.182", "Port": 22220}
    ],
    "Rules": ["netflix.com"],
    "RuleSets": {
      "hk": ["twitter.com"]
    }
  }
}`)
	if err := os.WriteFile(path, body, 0644); err != nil {
		t.Fatal(err)
	}

	cfg := New()
	if err := cfg.LoadFromPath(path); err != nil {
		t.Fatal(err)
	}
	if !cfg.Unlock.Enable {
		t.Fatal("unlock should be enabled")
	}
	if cfg.Unlock.DefaultOutbound != "sg" {
		t.Fatalf("default outbound = %q, want sg", cfg.Unlock.DefaultOutbound)
	}
	if len(cfg.Unlock.SOCKS) != 1 || cfg.Unlock.SOCKS[0].Port != 22220 {
		t.Fatalf("unexpected socks config: %#v", cfg.Unlock.SOCKS)
	}
	if len(cfg.Unlock.RuleSets["hk"]) != 1 || cfg.Unlock.RuleSets["hk"][0] != "twitter.com" {
		t.Fatalf("unexpected rule sets: %#v", cfg.Unlock.RuleSets)
	}
}
