// Package access verifies Cloudflare Access JWTs at the origin.
package access

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

const assertionHeader = "Cf-Access-Jwt-Assertion"

type Verifier struct {
	verifier *oidc.IDTokenVerifier
	allow    map[string]struct{}
}

// NewVerifier accepts tokens issued by teamDomain for the application aud.
// Only emails in allow (case-insensitive) are admitted.
func NewVerifier(teamDomain, aud string, allow []string, keySet oidc.KeySet) *Verifier {
	set := make(map[string]struct{}, len(allow))
	for _, email := range allow {
		set[strings.ToLower(email)] = struct{}{}
	}
	return &Verifier{
		verifier: oidc.NewVerifier(teamDomain, keySet, &oidc.Config{ClientID: aud}),
		allow:    set,
	}
}

// ParseAllowlist splits a comma-separated list of emails, dropping blanks.
func ParseAllowlist(csv string) ([]string, error) {
	var emails []string
	for _, part := range strings.Split(csv, ",") {
		if email := strings.TrimSpace(part); email != "" {
			emails = append(emails, email)
		}
	}
	if len(emails) == 0 {
		return nil, errors.New("allowlist is empty")
	}
	return emails, nil
}

// Middleware responds 401 for a missing or invalid Access JWT and 403 when
// the token's email is not allowlisted.
func (v *Verifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get(assertionHeader)
		if raw == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		token, err := v.verifier.Verify(r.Context(), raw)
		if err != nil {
			slog.Warn("access jwt rejected", "error", err)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var claims struct {
			Email string `json:"email"`
		}
		if err := token.Claims(&claims); err != nil || claims.Email == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if _, ok := v.allow[strings.ToLower(claims.Email)]; !ok {
			slog.Warn("access email not allowlisted")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
