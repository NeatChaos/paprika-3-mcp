// Command server runs paprika-3-mcp as a remote MCP server over streamable HTTP, e.g. on Vercel.
//
// Configuration comes from environment variables:
//   - PAPRIKA_USERNAME and PAPRIKA_PASSWORD: the Paprika 3 account
//   - PORT: the port to listen on (default 8080)
//
// Access mode (Cloud Run behind Cloudflare Access), selected by a non-empty ACCESS_AUD:
//   - ACCESS_AUD: the Access application audience tag
//   - ACCESS_TEAM_DOMAIN: https://<team>.cloudflareaccess.com
//   - ALLOWED_EMAILS: comma-separated emails admitted on /mcp
//   - CACHE_BUCKET: the GCS bucket holding the recipe cache
//
// Secret mode (Vercel), when ACCESS_AUD is empty:
//   - MCP_SECRET: a random string of at least 32 characters; the server only answers on /mcp/<MCP_SECRET>
package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/soggycactus/paprika-3-mcp/internal/access"
	"github.com/soggycactus/paprika-3-mcp/internal/cachestore"
	"github.com/soggycactus/paprika-3-mcp/internal/mcpserver"
)

var version = "dev" // set during build with -ldflags

const (
	minSecretLength = 32
	defaultPort     = "8080"
	cacheObject     = "recipe-cache.json"
)

type accessConfig struct {
	teamDomain string
	aud        string
	allow      []string
}

type config struct {
	username, password string
	port               string
	// access is non-nil in Access mode; otherwise secret guards the endpoint
	access      *accessConfig
	cacheBucket string
	secret      string
}

func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{
		username: getenv("PAPRIKA_USERNAME"),
		password: getenv("PAPRIKA_PASSWORD"),
		port:     getenv("PORT"),
	}
	if cfg.username == "" {
		return config{}, errors.New("PAPRIKA_USERNAME is required")
	}
	if cfg.password == "" {
		return config{}, errors.New("PAPRIKA_PASSWORD is required")
	}
	if cfg.port == "" {
		cfg.port = defaultPort
	}

	aud := getenv("ACCESS_AUD")
	if aud == "" {
		cfg.secret = getenv("MCP_SECRET")
		if cfg.secret == "" {
			return config{}, errors.New("ACCESS_AUD or MCP_SECRET is required")
		}
		if len(cfg.secret) < minSecretLength {
			return config{}, fmt.Errorf("MCP_SECRET must be at least %d characters", minSecretLength)
		}
		return cfg, nil
	}

	teamDomain := getenv("ACCESS_TEAM_DOMAIN")
	u, err := url.Parse(teamDomain)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return config{}, errors.New("ACCESS_TEAM_DOMAIN must be an https URL like https://<team>.cloudflareaccess.com")
	}
	allow, err := access.ParseAllowlist(getenv("ALLOWED_EMAILS"))
	if err != nil {
		return config{}, errors.New("ALLOWED_EMAILS must list at least one email")
	}
	cfg.cacheBucket = getenv("CACHE_BUCKET")
	if cfg.cacheBucket == "" {
		return config{}, errors.New("CACHE_BUCKET is required with ACCESS_AUD")
	}
	cfg.access = &accessConfig{teamDomain: teamDomain, aud: aud, allow: allow}
	return cfg, nil
}

// newHandler guards mcp with Access verification, or with the secret path outside Access mode.
func newHandler(cfg config, mcp http.Handler, keySet oidc.KeySet) http.Handler {
	if cfg.access == nil {
		return secretPath(cfg.secret, mcp)
	}
	guarded := access.NewVerifier(cfg.access.teamDomain, cfg.access.aud, cfg.access.allow, keySet).Middleware(mcp)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSuffix(r.URL.Path, "/") != "/mcp" {
			http.NotFound(w, r)
			return
		}
		guarded.ServeHTTP(w, r)
	})
}

// secretPath only passes requests for /mcp/<secret> to next. Anything else gets a 404, so the endpoint can't be discovered.
func secretPath(secret string, next http.Handler) http.Handler {
	want := []byte("/mcp/" + secret)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSuffix(r.URL.Path, "/")
		if subtle.ConstantTimeCompare([]byte(path), want) != 1 {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		logger.Error("invalid configuration", "err", err)
		os.Exit(1)
	}

	opts := mcpserver.NewServerOptions{
		Version:  version,
		Username: cfg.username,
		Password: cfg.password,
		Logger:   logger,
	}
	var keySet oidc.KeySet
	if cfg.access != nil {
		hc := &http.Client{Timeout: 30 * time.Second}
		opts.Store = cachestore.NewGCS(cfg.cacheBucket, cacheObject, hc, cachestore.DefaultEndpoint, cachestore.MetadataToken(hc))
		opts.OnDemandRefresh = true
		keySet = oidc.NewRemoteKeySet(context.Background(), cfg.access.teamDomain+"/cdn-cgi/access/certs")
	} else {
		// Recipes only, never credentials. Serverless instances only have a writable temp dir.
		opts.CachePath = filepath.Join(os.TempDir(), "paprika-3-mcp", "recipes.json")
	}

	s, err := mcpserver.NewServer(opts)
	if err != nil {
		logger.Error("failed to start paprika-3-mcp server", "err", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:              ":" + cfg.port,
		Handler:           newHandler(cfg, s.HTTPHandler(), keySet),
		ReadHeaderTimeout: 10 * time.Second,
	}
	logger.Info("starting remote mcp server", "version", version, "port", cfg.port, "access", cfg.access != nil)
	if err := srv.ListenAndServe(); err != nil {
		logger.Error("server stopped", "err", err)
		os.Exit(1)
	}
}
