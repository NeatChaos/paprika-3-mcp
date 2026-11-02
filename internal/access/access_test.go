package access

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
)

const teamDomain = "https://team.cloudflareaccess.com"

func newKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

type claims struct {
	Issuer   string   `json:"iss"`
	Audience []string `json:"aud"`
	Email    string   `json:"email"`
	Expiry   int64    `json:"exp"`
}

func sign(t *testing.T, key *rsa.PrivateKey, c claims) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	obj, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	token, err := obj.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func validClaims() claims {
	return claims{
		Issuer:   teamDomain,
		Audience: []string{"AUD1"},
		Email:    "Owner@Example.com",
		Expiry:   time.Now().Add(5 * time.Minute).Unix(),
	}
}

func newVerifier(key *rsa.PrivateKey) *Verifier {
	keySet := &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{key.Public()}}
	return NewVerifier(teamDomain, "AUD1", []string{"owner@example.com"}, keySet)
}

func serve(v *Verifier, header string) (*httptest.ResponseRecorder, bool) {
	called := false
	h := v.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	if header != "" {
		req.Header.Set("Cf-Access-Jwt-Assertion", header)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, called
}

func TestAccessValid(t *testing.T) {
	key := newKey(t)
	rec, called := serve(newVerifier(key), sign(t, key, validClaims()))
	if rec.Code != http.StatusOK || !called {
		t.Fatalf("code=%d called=%v", rec.Code, called)
	}
}

func TestAccessRejects(t *testing.T) {
	key := newKey(t)
	mutate := func(f func(*claims)) string {
		c := validClaims()
		f(&c)
		return sign(t, key, c)
	}
	cases := map[string]string{
		"no header":   "",
		"garbage":     "not-a-jwt",
		"wrong aud":   mutate(func(c *claims) { c.Audience = []string{"OTHER"} }),
		"wrong iss":   mutate(func(c *claims) { c.Issuer = "https://evil.example.com" }),
		"expired":     mutate(func(c *claims) { c.Expiry = time.Now().Add(-time.Hour).Unix() }),
		"wrong key":   sign(t, newKey(t), validClaims()),
		"empty email": mutate(func(c *claims) { c.Email = "" }),
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			rec, called := serve(newVerifier(key), header)
			if rec.Code != http.StatusUnauthorized || called {
				t.Fatalf("code=%d called=%v", rec.Code, called)
			}
		})
	}
}

func TestAccessNotAllowlisted(t *testing.T) {
	key := newKey(t)
	c := validClaims()
	c.Email = "stranger@example.com"
	rec, called := serve(newVerifier(key), sign(t, key, c))
	if rec.Code != http.StatusForbidden || called {
		t.Fatalf("code=%d called=%v", rec.Code, called)
	}
}

func TestParseAllowlist(t *testing.T) {
	got, err := ParseAllowlist(" A@x.com, ,b@x.com ")
	if err != nil || len(got) != 2 || got[0] != "A@x.com" || got[1] != "b@x.com" {
		t.Fatalf("got %v, %v", got, err)
	}
	for _, in := range []string{"", " , "} {
		if _, err := ParseAllowlist(in); err == nil {
			t.Errorf("ParseAllowlist(%q) should error", in)
		}
	}
}
