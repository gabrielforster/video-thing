package main

import (
	"fmt"
	"sort"
	"strings"
)

type Config struct {
	DatabaseURL        string
	RawBucket          string
	ProcessedBucket    string
	AWSEndpointURL     string
	PublicAssetBaseURL string
	Port               string
}

func LoadConfig(getenv func(string) string) (Config, error) {
	cfg := Config{
		DatabaseURL:        getenv("DATABASE_URL"),
		RawBucket:          getenv("RAW_BUCKET"),
		ProcessedBucket:    getenv("PROCESSED_BUCKET"),
		AWSEndpointURL:     getenv("AWS_ENDPOINT_URL"),
		PublicAssetBaseURL: strings.TrimSuffix(getenv("PUBLIC_ASSET_BASE_URL"), "/"),
		Port:               getenv("PORT"),
	}
	if cfg.Port == "" {
		cfg.Port = "8080"
	}

	var missing []string
	for name, value := range map[string]string{
		"DATABASE_URL":          cfg.DatabaseURL,
		"RAW_BUCKET":            cfg.RawBucket,
		"PROCESSED_BUCKET":      cfg.ProcessedBucket,
		"PUBLIC_ASSET_BASE_URL": cfg.PublicAssetBaseURL,
	} {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return Config{}, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}
