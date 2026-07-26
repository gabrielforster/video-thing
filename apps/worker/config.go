package main

import (
	"fmt"
	"sort"
	"strings"
)

type Config struct {
	DatabaseURL     string
	QueueURL        string
	ProcessedBucket string
	AWSEndpointURL  string
}

func LoadConfig(getenv func(string) string) (Config, error) {
	cfg := Config{
		DatabaseURL:     getenv("DATABASE_URL"),
		QueueURL:        getenv("QUEUE_URL"),
		ProcessedBucket: getenv("PROCESSED_BUCKET"),
		AWSEndpointURL:  getenv("AWS_ENDPOINT_URL"),
	}

	var missing []string
	for name, value := range map[string]string{
		"DATABASE_URL":     cfg.DatabaseURL,
		"QUEUE_URL":        cfg.QueueURL,
		"PROCESSED_BUCKET": cfg.ProcessedBucket,
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
