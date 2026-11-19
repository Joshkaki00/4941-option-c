// Package auth verifies SSO bearer tokens and turns them into an Identity.
// Identity comes only from the token. Request bodies and headers never supply user ids.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

// CompanyWideAudience is granted to every authenticated employee.
const CompanyWideAudience = "all-employees"

// ErrInvalidToken is returned for any token that does not verify.
var ErrInvalidToken = errors.New("auth: invalid token")

// Identity is the verified caller.
type Identity struct {
	Subject   string
	Audiences []string
}

// Verifier turns a raw bearer token into an Identity.
type Verifier interface {
	Verify(ctx context.Context, rawToken string) (Identity, error)
}

// AudiencesFor maps SSO groups to audiences: groups, plus all-employees, deduplicated.
func AudiencesFor(groups []string) []string {
	set := map[string]bool{CompanyWideAudience: true}
	for _, g := range groups {
		g = strings.TrimSpace(g)
		if g != "" {
			set[g] = true
		}
	}
	out := make([]string, 0, len(set))
	for a := range set {
		out = append(out, a)
	}
	slices.Sort(out)
	return out
}

// OIDC verifies ID tokens from a configured provider (proposal.md §13).
type OIDC struct {
	verifier    *oidc.IDTokenVerifier
	groupsClaim string
}

// NewOIDC discovers the provider and builds a verifier bound to clientID.
func NewOIDC(ctx context.Context, issuer, clientID, groupsClaim string) (*OIDC, error) {
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	return &OIDC{
		verifier:    provider.VerifierContext(ctx, &oidc.Config{ClientID: clientID}),
		groupsClaim: groupsClaim,
	}, nil
}

// Verify implements Verifier.
func (o *OIDC) Verify(ctx context.Context, raw string) (Identity, error) {
	tok, err := o.verifier.Verify(ctx, raw)
	if err != nil {
		return Identity{}, ErrInvalidToken
	}
	var claims map[string]any
	if err := tok.Claims(&claims); err != nil {
		return Identity{}, ErrInvalidToken
	}
	return Identity{Subject: tok.Subject, Audiences: AudiencesFor(stringList(claims[o.groupsClaim]))}, nil
}

func stringList(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return strings.Split(t, ",")
	default:
		return nil
	}
}

// StaticTokens is a dev-only verifier. The server enables it only when no OIDC issuer is configured.
type StaticTokens map[string]Identity

// Verify implements Verifier.
func (s StaticTokens) Verify(_ context.Context, raw string) (Identity, error) {
	id, ok := s[raw]
	if !ok {
		return Identity{}, ErrInvalidToken
	}
	return id, nil
}

type ctxKey struct{}

// FromContext returns the Identity set by Require.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(ctxKey{}).(Identity)
	return id, ok
}

// Require rejects requests without a valid bearer token with 401, then calls next.
func Require(v Verifier, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
			writeUnauthorized(w)
			return
		}
		id, err := v.Verify(r.Context(), strings.TrimSpace(token))
		if err != nil {
			writeUnauthorized(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="ask-the-handbook"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
}
