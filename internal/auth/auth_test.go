package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestAudiencesForAddsCompanyWideAndDedupes(t *testing.T) {
	got := AudiencesFor([]string{"engineering", "managers", "engineering", ""})
	want := []string{"all-employees", "engineering", "managers"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAudiencesForNoGroupsStillCompanyWide(t *testing.T) {
	if got := AudiencesFor(nil); !reflect.DeepEqual(got, []string{"all-employees"}) {
		t.Fatalf("got %v", got)
	}
}

func TestRequireRejectsMissingHeader(t *testing.T) {
	h := Require(StaticTokens{"t": {Subject: "u"}}, okHandler(t))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/x", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestRequireRejectsUnknownToken(t *testing.T) {
	h := Require(StaticTokens{"good": {Subject: "u"}}, okHandler(t))
	req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	req.Header.Set("Authorization", "Bearer bad")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestRequireRejectsNonBearerScheme(t *testing.T) {
	h := Require(StaticTokens{"good": {Subject: "u"}}, okHandler(t))
	req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	req.Header.Set("Authorization", "Basic good")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestRequirePassesIdentityToHandler(t *testing.T) {
	want := Identity{Subject: "user-1", Audiences: []string{"all-employees", "hr-only"}}
	var got Identity
	h := Require(StaticTokens{"good": want}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := FromContext(r.Context())
		if !ok {
			t.Error("identity missing from context")
		}
		got = id
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	req.Header.Set("Authorization", "Bearer good")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", rec.Code)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("identity mismatch: %+v", got)
	}
}

func TestStaticTokensVerifyUnknownReturnsErr(t *testing.T) {
	if _, err := (StaticTokens{}).Verify(context.Background(), "x"); err == nil {
		t.Fatal("want error for unknown token")
	}
}

func okHandler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler must not run for rejected requests")
	})
}
