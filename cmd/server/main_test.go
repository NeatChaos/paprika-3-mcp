package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"

	"github.com/mark3labs/mcp-go/server"
)

const (
	testSecret = "0123456789abcdef0123456789abcdef"
	testTeam   = "https://team.cloudflareaccess.com"
	testAud    = "AUD1"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func accessEnv() map[string]string {
	return map[string]string{
		"PAPRIKA_USERNAME":   "user@example.com",
		"PAPRIKA_PASSWORD":   "hunter2-not-real",
		"ACCESS_TEAM_DOMAIN": testTeam,
		"ACCESS_AUD":         testAud,
		"ALLOWED_EMAILS":     "owner@example.com",
		"CACHE_BUCKET":       "bkt",
	}
}

func secretEnv(secret string) map[string]string {
	return map[string]string{"PAPRIKA_USERNAME": "u", "PAPRIKA_PASSWORD": "p", "MCP_SECRET": secret}
}

func TestLoadConfig(t *testing.T) {
	without := func(key string) map[string]string {
		env := accessEnv()
		delete(env, key)
		return env
	}
	with := func(key, value string) map[string]string {
		env := accessEnv()
		env[key] = value
		return env
	}
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"access mode ok", accessEnv(), ""},
		{"secret mode ok", secretEnv(testSecret), ""},
		{"missing username", without("PAPRIKA_USERNAME"), "PAPRIKA_USERNAME"},
		{"missing password", without("PAPRIKA_PASSWORD"), "PAPRIKA_PASSWORD"},
		{"neither aud nor secret", without("ACCESS_AUD"), "ACCESS_AUD"},
		{"empty aud and no secret", with("ACCESS_AUD", ""), "ACCESS_AUD"},
		{"missing team domain", without("ACCESS_TEAM_DOMAIN"), "ACCESS_TEAM_DOMAIN"},
		{"http team domain", with("ACCESS_TEAM_DOMAIN", "http://team.cloudflareaccess.com"), "ACCESS_TEAM_DOMAIN"},
		{"hostless team domain", with("ACCESS_TEAM_DOMAIN", "https://"), "ACCESS_TEAM_DOMAIN"},
		{"missing allowed emails", without("ALLOWED_EMAILS"), "ALLOWED_EMAILS"},
		{"blank allowed emails", with("ALLOWED_EMAILS", " , "), "ALLOWED_EMAILS"},
		{"missing bucket", without("CACHE_BUCKET"), "CACHE_BUCKET"},
		{"short secret", secretEnv("short"), "MCP_SECRET"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadConfig(envFrom(tt.env))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %v should name %s", err, tt.wantErr)
			}
			if pw := tt.env["PAPRIKA_PASSWORD"]; pw != "" && strings.Contains(err.Error(), pw) {
				t.Errorf("error leaks the password: %v", err)
			}
		})
	}
}

func TestLoadConfigModes(t *testing.T) {
	env := accessEnv()
	env["MCP_SECRET"] = testSecret
	cfg, err := loadConfig(envFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.access == nil || cfg.secret != "" {
		t.Errorf("ACCESS_AUD set: access mode must win and MCP_SECRET be ignored, got %+v", cfg)
	}
	if cfg.port != "8080" {
		t.Errorf("default port = %q", cfg.port)
	}
	env["PORT"] = "9000"
	cfg, err = loadConfig(envFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.port != "9000" {
		t.Errorf("port = %q", cfg.port)
	}
}

func TestAccessModeRoutes(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"iss": testTeam, "aud": []string{testAud}, "email": "owner@example.com",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	obj, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	jwt, err := obj.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfig(envFrom(accessEnv()))
	if err != nil {
		t.Fatal(err)
	}
	mcp := server.NewStreamableHTTPServer(server.NewMCPServer("test", "0"), server.WithStateLess(true))
	keySet := &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{key.Public()}}
	srv := httptest.NewServer(newHandler(cfg, mcp, keySet))
	defer srv.Close()

	post := func(path, token string) *http.Response {
		body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
		req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Cf-Access-Jwt-Assertion", token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}

	if got := post("/mcp", "").StatusCode; got != http.StatusUnauthorized {
		t.Errorf("no header: %d", got)
	}
	if got := post("/mcp", "garbage").StatusCode; got != http.StatusUnauthorized {
		t.Errorf("bad token: %d", got)
	}
	if got := post("/other", jwt).StatusCode; got != http.StatusNotFound {
		t.Errorf("other path: %d", got)
	}
	resp := post("/mcp", jwt)
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"serverInfo"`) {
		t.Errorf("initialize: %d %s", resp.StatusCode, b)
	}
}

func TestSecretModeRoutes(t *testing.T) {
	cfg, err := loadConfig(envFrom(secretEnv(testSecret)))
	if err != nil {
		t.Fatal(err)
	}
	h := newHandler(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }), nil)
	for path, want := range map[string]int{"/mcp/" + testSecret: http.StatusTeapot, "/mcp": http.StatusNotFound} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d want %d", path, rec.Code, want)
		}
	}
}

func TestSecretPath(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	handler := secretPath(secret, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	tests := []struct {
		path string
		want int
	}{
		{"/mcp/" + secret, http.StatusTeapot},
		{"/mcp/" + secret + "/", http.StatusTeapot},
		{"/mcp/" + secret[:31], http.StatusNotFound},
		{"/mcp/" + secret + "x", http.StatusNotFound},
		{"/mcp/" + secret + "/other", http.StatusNotFound},
		{"/mcp", http.StatusNotFound},
		{"/", http.StatusNotFound},
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, tt.path, nil))
		if rec.Code != tt.want {
			t.Errorf("%s: got %d, want %d", tt.path, rec.Code, tt.want)
		}
	}
}
