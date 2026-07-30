package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	gin.SetMode(gin.ReleaseMode)

	cfg, err := LoadConfig(os.Getenv)
	if err != nil {
		slog.Error("config", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		slog.Error("aws config", "error", err)
		os.Exit(1)
	}
	s3Client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		// LocalStack needs an explicit endpoint and path-style addressing;
		// in AWS both are empty/false and the SDK defaults apply.
		if cfg.AWSEndpointURL != "" {
			o.BaseEndpoint = aws.String(cfg.AWSEndpointURL)
			o.UsePathStyle = true
		}
	})

	h := newHandlers(newPGStore(pool), NewPresigner(s3Client, cfg.RawBucket, 15*time.Minute),
		NewS3AssetCleaner(s3Client, cfg.ProcessedBucket), cfg.RawBucket, cfg.PublicAssetBaseURL)

	r := newRouter(h, func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		return pool.Ping(ctx)
	})

	slog.Info("api listening", "port", cfg.Port)
	if err := r.Run(":" + cfg.Port); err != nil {
		slog.Error("listen", "error", err)
		os.Exit(1)
	}
}
