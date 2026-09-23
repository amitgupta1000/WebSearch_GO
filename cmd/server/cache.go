package main

import (
	"context"
	"log"
	"net/url"
	"strings"

	"github.com/amitgupta1000/WebSearch_GO/internal/cache"
	"github.com/amitgupta1000/WebSearch_GO/internal/config"
)

// buildCacheStore selects the cache backend named by configuration: Redis or
// Cloud Storage when configured, the in-process cache otherwise.
//
// A shared backend is what turns the cache from "warm within one process" into
// real caching: search results and scraped pages survive a restart and are shared
// by every instance.
//
// A cache is an optimization, not a dependency, so a backend that cannot be
// reached falls back to memory rather than failing startup — but it says so
// loudly, because silently running without the configured backend is exactly the
// kind of misconfiguration that is hard to notice.
func buildCacheStore(ctx context.Context, cfg *config.Config) cache.Store {
	switch cfg.CacheBackendName() {
	case "redis":
		connectionURL := cfg.RedisConnectionURL()
		if connectionURL == "" {
			log.Printf("[WARN] CACHE_BACKEND=redis but neither REDIS_URL nor REDIS_HOST is set; falling back to the in-memory cache")
			break
		}
		store, err := cache.NewRedisCache(ctx, connectionURL, cfg.RedisKeyPrefix)
		if err != nil {
			log.Printf("[WARN] Redis cache unavailable (%v); falling back to the in-memory cache", err)
			break
		}
		// Only the host is logged: the connection URL may carry a password.
		log.Printf("Cache backend: redis (%s, prefix %q)", redactedTarget(connectionURL), cfg.RedisKeyPrefix)
		return store
	case "gcs":
		if strings.TrimSpace(cfg.GCSCacheBucket) == "" {
			log.Printf("[WARN] CACHE_BACKEND=gcs but GCS_CACHE_BUCKET is not set; falling back to the in-memory cache")
			break
		}
		store, err := cache.NewGCSCache(ctx, cfg.GCSCacheBucket, cfg.GCSCachePrefix)
		if err != nil {
			log.Printf("[WARN] GCS cache unavailable (%v); falling back to the in-memory cache", err)
			break
		}
		log.Printf("Cache backend: gcs (bucket %s, prefix %q, credentials: %s)",
			cfg.GCSCacheBucket, cfg.GCSCachePrefix, credentialSource(cfg))
		return store
	default:
		if backend := cfg.CacheBackendName(); backend != "memory" {
			log.Printf("[WARN] Unknown CACHE_BACKEND %q; using the in-memory cache", backend)
		}
	}
	log.Printf("Cache backend: memory (max %d entries)", cfg.CacheMaxEntries)
	return cache.NewMemoryCache(cfg.CacheMaxEntries)
}

// credentialSource describes where the Google client libraries will take their
// credentials from, which is the first thing to check when a GCS cache cannot
// authenticate. An explicit service-account file wins; otherwise the libraries
// fall back to the metadata server or a local `gcloud auth application-default
// login`.
func credentialSource(cfg *config.Config) string {
	if file := strings.TrimSpace(cfg.GoogleCredentialsFile); file != "" {
		return "service-account file from GOOGLE_APPLICATION_CREDENTIALS"
	}
	return "ambient (metadata server or gcloud ADC)"
}

// redactedTarget reduces a connection URL to its host for logging, so credentials
// in a Redis URL never reach the log.
func redactedTarget(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return "(configured)"
	}
	return parsed.Host
}
