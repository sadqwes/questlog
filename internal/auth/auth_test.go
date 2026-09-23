package auth

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type memSessions struct {
	mu sync.Mutex
	m  map[string]time.Time
}

func (s *memSessions) CreateSession(_ context.Context, h []byte, exp time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[string(h)] = exp
	return nil
}
func (s *memSessions) SessionValid(_ context.Context, h []byte) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.m[string(h)]
	return ok && exp.After(time.Now()), nil
}
func (s *memSessions) DeleteSession(_ context.Context, h []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, string(h))
	return nil
}

const (
	testUser  = "sadqwes"
	testPass  = "correct horse battery staple"
	testToken = "0123456789abcdef0123456789abcdef-token"
)

func newTestAuth(t *testing.T) (*httptest.Server, *memSessions) {
	t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte(testPass), bcrypt.MinCost) // MinCost — чтобы тесты были быстрыми
	sessions := &memSessions{m: map[string]time.Time{}}
	a, err := New(Config{User: testUser, PasswordHash: string(hash), APIToken: testToken}, sessions, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a.Register(mux)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("app")) })
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("{}")) })
	mux.HandleFunc("PUT /api/hero", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("{}")) })
	ts := httptest.NewServer(a.Protect(mux))
	t.Cleanup(ts.Close)
	return ts, sessions
}

// client не ходит по редиректам — нам важны сами ответы.
var client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func login(t *testing.T, ts *httptest.Server, user, pass string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/login", strings.NewReader(url.Values{"username": {user}, "password": {pass}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", ts.URL)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func get(t *testing.T, ts *httptest.Server, method, path string, mod func(*http.Request)) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader("{}"))
	if mod != nil {
		mod(req)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestUnauthenticated(t *testing.T) {
	ts, _ := newTestAuth(t)
	if r := get(t, ts, "GET", "/api/state", nil); r.StatusCode != 401 {
		t.Errorf("api without auth = %d, want 401", r.StatusCode)
	}
	if r := get(t, ts, "GET", "/", nil); r.StatusCode != 303 || r.Header.Get("Location") != "/login" {
		t.Errorf("page without auth = %d %q, want redirect to /login", r.StatusCode, r.Header.Get("Location"))
	}
	if r := get(t, ts, "GET", "/healthz", nil); r.StatusCode != 200 {
		t.Errorf("healthz must stay public, got %d", r.StatusCode)
	}
	if r := get(t, ts, "GET", "/login", nil); r.StatusCode != 200 {
		t.Errorf("login page = %d", r.StatusCode)
	}
}

func TestLoginSessionLogout(t *testing.T) {
	ts, sessions := newTestAuth(t)
	resp := login(t, ts, testUser, testPass)
	if resp.StatusCode != 303 {
		t.Fatalf("login = %d, want 303", resp.StatusCode)
	}
	var c *http.Cookie
	for _, x := range resp.Cookies() {
		if x.Name == cookieName {
			c = x
		}
	}
	if c == nil || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie = %+v", c)
	}
	for h := range sessions.m {
		if h == c.Value {
			t.Fatal("raw token stored in sessions, want only its hash")
		}
	}
	withCookie := func(r *http.Request) { r.AddCookie(c) }
	if r := get(t, ts, "GET", "/api/state", withCookie); r.StatusCode != 200 {
		t.Errorf("api with session = %d", r.StatusCode)
	}
	logout := get(t, ts, "POST", "/logout", func(r *http.Request) { r.AddCookie(c); r.Header.Set("Origin", ts.URL) })
	if logout.StatusCode != 303 {
		t.Errorf("logout = %d", logout.StatusCode)
	}
	if r := get(t, ts, "GET", "/api/state", withCookie); r.StatusCode != 401 {
		t.Errorf("api after logout = %d, want 401", r.StatusCode)
	}
}

func TestWrongPasswordAndRateLimit(t *testing.T) {
	ts, _ := newTestAuth(t)
	for i := 0; i < maxFailures; i++ {
		if r := login(t, ts, testUser, "nope"); r.StatusCode != 401 {
			t.Fatalf("attempt %d = %d, want 401", i, r.StatusCode)
		}
	}
	// даже верный пароль не пускает, пока идёт блокировка
	if r := login(t, ts, testUser, testPass); r.StatusCode != 429 {
		t.Errorf("after %d failures = %d, want 429", maxFailures, r.StatusCode)
	}
}

func TestBearerToken(t *testing.T) {
	ts, _ := newTestAuth(t)
	ok := get(t, ts, "PUT", "/api/hero", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+testToken) })
	if ok.StatusCode != 200 {
		t.Errorf("valid token = %d", ok.StatusCode)
	}
	bad := get(t, ts, "GET", "/api/state", func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") })
	if bad.StatusCode != 401 {
		t.Errorf("wrong token = %d, want 401", bad.StatusCode)
	}
}

func TestCrossOriginWriteBlocked(t *testing.T) {
	ts, _ := newTestAuth(t)
	resp := login(t, ts, testUser, testPass)
	c := resp.Cookies()[0]
	evil := get(t, ts, "PUT", "/api/hero", func(r *http.Request) { r.AddCookie(c); r.Header.Set("Origin", "https://evil.example") })
	if evil.StatusCode != 403 {
		t.Errorf("cross-origin write = %d, want 403", evil.StatusCode)
	}
	same := get(t, ts, "PUT", "/api/hero", func(r *http.Request) { r.AddCookie(c); r.Header.Set("Origin", ts.URL) })
	if same.StatusCode != 200 {
		t.Errorf("same-origin write = %d", same.StatusCode)
	}
}

func TestNewRejectsWeakConfig(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &memSessions{m: map[string]time.Time{}}
	if _, err := New(Config{User: "u", PasswordHash: "plain-text"}, s, log); err == nil {
		t.Error("plain password accepted as hash")
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("x"), bcrypt.MinCost)
	if _, err := New(Config{User: "u", PasswordHash: string(hash), APIToken: "short"}, s, log); err == nil {
		t.Error("short api token accepted")
	}
}

func TestNullOriginRejected(t *testing.T) {
	// "Origin: null" приходит из sandbox-iframe и при Referrer-Policy: no-referrer — своим такой запрос не считаем
	ts, _ := newTestAuth(t)
	req, _ := http.NewRequest("POST", ts.URL+"/login", strings.NewReader(url.Values{"username": {testUser}, "password": {testPass}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "null")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("Origin: null login = %d, want 403", resp.StatusCode)
	}
}
