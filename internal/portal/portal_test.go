package portal

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"rf4server/internal/store"
)

func TestPortalIdentityPurchaseReplayAndLogout(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "portal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	for _, uid := range []int64{42, 43} {
		if _, err := db.EnsureProfile(context.Background(), uid); err != nil {
			t.Fatal(err)
		}
	}
	for uid, username := range map[int64]string{42: "angler_one", 43: "angler_two"} {
		hash, err := bcrypt.GenerateFromPassword([]byte("correct horse battery"), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.SetPortalAccount(context.Background(), uid, username, string(hash)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.SQL.Exec(`UPDATE profile SET gold=500`); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	call := func(method, path, token, body, remote, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://game.test"+path, strings.NewReader(body))
		r.RemoteAddr = remote
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	const own = "192.0.2.1:1000"
	if w := call("GET", "/portal/api/profile", "", "", own, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	httpRequest := httptest.NewRequest("http", "http://game.test/portal/api/session", strings.NewReader(`{"username":"angler_one","password":"correct horse battery"}`))
	httpRequest.RemoteAddr = own
	httpResponse := httptest.NewRecorder()
	s.ServeHTTP(httpResponse, httpRequest)
	if httpResponse.Code != 400 {
		t.Fatal("portal credentials were accepted over cleartext HTTP")
	}
	if w := call("POST", "/portal/api/session", "", "{}", own, "https://other.test"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := call("POST", "/portal/api/session", "", `{"username":"angler_one","password":"incorrect pass"}`, own, ""); w.Code != 401 {
		t.Fatal("invalid password accepted")
	}
	w := call("POST", "/portal/api/session", "", `{"username":"angler_one","password":"correct horse battery"}`, own, "")
	var result struct {
		Token string `json:"token"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Token == "" {
		t.Fatal(w.Body.String())
	}
	token := result.Token
	if w := call("GET", "/portal/api/profile", token, "", "192.0.2.2:1000", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"userId":42`) {
		t.Fatal(w.Body.String())
	}
	second := call("POST", "/portal/api/session", "", `{"username":"angler_two","password":"correct horse battery"}`, own, "")
	var secondResult struct {
		Token string `json:"token"`
	}
	if second.Code != 200 || json.Unmarshal(second.Body.Bytes(), &secondResult) != nil || secondResult.Token == "" {
		t.Fatal(second.Body.String())
	}
	if w := call("GET", "/portal/api/profile", secondResult.Token, "", own, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"userId":43`) {
		t.Fatal("same network account resolved to the wrong player: " + w.Body.String())
	}
	if w := call("POST", "/portal/api/membership/purchase", token, `{"uid":43,"planID":"premium_1d","requestID":"purchase-0001"}`, own, ""); w.Code != 400 {
		t.Fatal("accepted caller-selected uid")
	}
	for range 2 {
		if w := call("POST", "/portal/api/membership/purchase", token, `{"planID":"premium_1d","requestID":"purchase-0001"}`, own, ""); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	p, err := db.GetProfile(context.Background(), 42)
	other, _ := db.GetProfile(context.Background(), 43)
	if err != nil || p.Gold != 400 || p.MembershipExp <= time.Now().Unix() || other.Gold != 500 {
		t.Fatalf("purchase identity/replay failed: %+v %+v %v", p, other, err)
	}
	if w := call("DELETE", "/portal/api/session", token, "", own, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := call("GET", "/portal/api/profile", token, "", own, ""); w.Code != 401 {
		t.Fatal("logout did not revoke session")
	}
	if err := db.SetBanned(context.Background(), 43, true); err != nil {
		t.Fatal(err)
	}
	if w := call("GET", "/portal/api/profile", secondResult.Token, "", own, ""); w.Code != 401 {
		t.Fatal("banned player session remained valid")
	}
	if err := db.SetBanned(context.Background(), 43, false); err != nil {
		t.Fatal(err)
	}
	if w := call("GET", "/portal/api/profile", secondResult.Token, "", own, ""); w.Code != 401 {
		t.Fatal("unbanning restored an old session")
	}
	for _, path := range []string{"/portal/", "/portal/local.js", "/portal/local.css"} {
		if w := call("GET", path, "", "", own, ""); w.Code != 200 {
			t.Fatalf("missing asset %s: %d", path, w.Code)
		}
	}
	if w := call("GET", "/portal/api/debug", "", "", own, ""); w.Code != 404 {
		t.Fatal("excluded debug endpoint exposed")
	}
}
