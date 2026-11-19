package tests

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/Joshkaki00/4941-option-c/internal/auth"
)

func TestAudiencesAlwaysIncludeCompanyWide(t *testing.T) {
	if got := auth.AudiencesFor(nil); !reflect.DeepEqual(got, []string{"all-employees"}) {
		t.Fatalf("no groups = %v", got)
	}
}

func TestAudiencesDedupeTrimSortAndDropEmpty(t *testing.T) {
	got := auth.AudiencesFor([]string{" hr-only ", "", "all-employees", "eng", "eng"})
	want := []string{"all-employees", "eng", "hr-only"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("audiences = %v, want %v", got, want)
	}
}

func TestStaticTokensVerifyKnownAndRejectUnknown(t *testing.T) {
	st := auth.StaticTokens{"tok-a": {Subject: "alice", Audiences: []string{"all-employees"}}}
	id, err := st.Verify(context.Background(), "tok-a")
	if err != nil || id.Subject != "alice" {
		t.Fatalf("known token: id=%+v err=%v", id, err)
	}
	if _, err := st.Verify(context.Background(), "nope"); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("unknown token err = %v, want ErrInvalidToken", err)
	}
	if _, err := st.Verify(context.Background(), ""); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("empty token err = %v, want ErrInvalidToken", err)
	}
}

func TestFromContextAbsentReturnsFalse(t *testing.T) {
	if _, ok := auth.FromContext(context.Background()); ok {
		t.Fatal("empty context must not carry an identity")
	}
}

var testVerifier = auth.StaticTokens{
	"good": {Subject: "alice", Audiences: []string{"all-employees"}},
}

func requestWith(t *testing.T, h string) (*httptest.ResponseRecorder, *auth.Identity) {
	t.Helper()
	var seen *auth.Identity
	handler := auth.Require(testVerifier, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := auth.FromContext(r.Context())
		seen = &id
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if h != "" {
		req.Header.Set("Authorization", h)
	}
	handler.ServeHTTP(rec, req)
	return rec, seen
}

func TestRequireRejectsMissingOrMalformedAuthorization(t *testing.T) {
	for _, h := range []string{"", "Basic good", "Bearer", "Bearer ", "good"} {
		rec, seen := requestWith(t, h)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("header %q: status %d, want 401", h, rec.Code)
		}
		if seen != nil {
			t.Fatalf("header %q reached the next handler", h)
		}
	}
}

func TestRequireRejectsUnknownToken(t *testing.T) {
	rec, seen := requestWith(t, "Bearer bad")
	if rec.Code != http.StatusUnauthorized || seen != nil {
		t.Fatalf("unknown token: status %d, reached next=%v", rec.Code, seen != nil)
	}
}

func TestRequireUnauthorizedResponseShape(t *testing.T) {
	rec, _ := requestWith(t, "")
	if got := rec.Header().Get("WWW-Authenticate"); got != `Bearer realm="ask-the-handbook"` {
		t.Fatalf("WWW-Authenticate = %q", got)
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("content type = %q", rec.Header().Get("Content-Type"))
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] != "unauthorized" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestRequireAcceptsValidTokenAndAttachesIdentity(t *testing.T) {
	rec, seen := requestWith(t, "Bearer good")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if seen == nil || seen.Subject != "alice" {
		t.Fatalf("identity not attached: %+v", seen)
	}
}

func TestRequireSchemeIsCaseInsensitiveAndTrimsToken(t *testing.T) {
	rec, _ := requestWith(t, "bearer   good  ")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
