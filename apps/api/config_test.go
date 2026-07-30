package main

import "testing"
import "strings"

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadConfigDefaultsPort(t *testing.T) {
	cfg, err := LoadConfig(env(map[string]string{
		"DATABASE_URL":          "postgres://localhost/db",
		"RAW_BUCKET":            "raw",
		"PROCESSED_BUCKET":      "processed",
		"PUBLIC_ASSET_BASE_URL": "http://localhost:4566/processed",
	}))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Port != "8080" {
		t.Fatalf("Port = %q, want 8080", cfg.Port)
	}
	if cfg.AWSEndpointURL != "" {
		t.Fatalf("AWSEndpointURL = %q, want empty", cfg.AWSEndpointURL)
	}
}

func TestLoadConfigRequiresVars(t *testing.T) {
	_, err := LoadConfig(env(map[string]string{"RAW_BUCKET": "raw"}))
	if err == nil {
		t.Fatal("expected error for missing DATABASE_URL")
	}
	if got := err.Error(); !strings.Contains(got, "DATABASE_URL") || !strings.Contains(got, "PUBLIC_ASSET_BASE_URL") || !strings.Contains(got, "PROCESSED_BUCKET") {
		t.Fatalf("error %q should name every missing variable", got)
	}
}
