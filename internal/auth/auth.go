// Package auth — вход в questlog: одна пользовательница, пароль в виде bcrypt-хеша,
// сессия в cookie и отдельный API-токен для наставника (Claude).
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	cookieName  = "ql_session"
	maxFailures = 5
	lockout     = 15 * time.Minute
)

// Sessions — хранилище сессий; в базе лежит только SHA-256 от токена.
type Sessions interface {
	CreateSession(ctx context.Context, tokenHash []byte, passwordFP string, expires time.Time) error
	SessionValid(ctx context.Context, tokenHash []byte, passwordFP string) (bool, error)
	DeleteSession(ctx context.Context, tokenHash []byte) error
	DeleteAllSessions(ctx context.Context) error
}

type Config struct {
	User         string
	PasswordHash string        // bcrypt, см. `questlog hash-password`
	APIToken     string        // для Authorization: Bearer; пустой — API только через сессию
	SessionTTL   time.Duration // сколько живёт вход
}

type Auth struct {
	cfg      Config
	sessions Sessions
	log      *slog.Logger
	limiter  *limiter
	page     *template.Template
	fp       string // отпечаток текущего пароля: сменили пароль — старые сессии недействительны
}

//go:embed login.html
var loginHTML string

func New(cfg Config, s Sessions, log *slog.Logger) (*Auth, error) {
	if cfg.User == "" || cfg.PasswordHash == "" {
		return nil, errors.New("QUESTLOG_USER and QUESTLOG_PASSWORD_HASH are required")
	}
	if _, err := bcrypt.Cost([]byte(cfg.PasswordHash)); err != nil {
		return nil, errors.New("QUESTLOG_PASSWORD_HASH is not a bcrypt hash (make one with `questlog hash-password`)")
	}
	if cfg.APIToken != "" && len(cfg.APIToken) < 32 {
		return nil, errors.New("QUESTLOG_API_TOKEN must be at least 32 characters")
	}
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = 30 * 24 * time.Hour
	}
	return &Auth{
		cfg: cfg, sessions: s, log: log,
		limiter: newLimiter(maxFailures, lockout),
		page:    template.Must(template.New("login").Parse(loginHTML)),
		fp:      passwordFingerprint(cfg.PasswordHash),
	}, nil
}

// passwordFingerprint — первые 8 байт SHA-256 от bcrypt-хеша. Сам хеш в таблицу сессий не попадает.
func passwordFingerprint(hash string) string {
	sum := sha256.Sum256([]byte(hash))
	return hex.EncodeToString(sum[:8])
}

// HashPassword — для команды `questlog hash-password`.
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	return string(h), err
}

func (a *Auth) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /login", a.loginPage)
	mux.HandleFunc("POST /login", a.login)
	mux.HandleFunc("POST /logout", a.logout)
	mux.HandleFunc("POST /logout-all", a.logoutAll)
}

// Пути, доступные без входа: сама страница входа, её стили и пробы Kubernetes.
var public = map[string]bool{
	"/login": true, "/style.css": true, "/healthz": true, "/readyz": true,
}

// Protect пропускает запрос дальше только с валидной сессией или API-токеном.
func (a *Auth) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case public[r.URL.Path]:
		case a.bearerOK(r):
			// токен не отправляется браузером сам по себе — CSRF ему не страшен
		case a.sessionOK(r):
			if !safeMethod(r.Method) && !sameOrigin(r) {
				http.Error(w, "cross-origin request blocked", http.StatusForbidden)
				return
			}
		case strings.HasPrefix(r.URL.Path, "/api/"):
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"нужно войти"}`))
			return
		default:
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Auth) bearerOK(r *http.Request) bool {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || a.cfg.APIToken == "" {
		return false
	}
	// сравниваем хеши: одинаковая длина и постоянное время — без утечки по таймингу
	got, want := sha256.Sum256([]byte(token)), sha256.Sum256([]byte(a.cfg.APIToken))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

func (a *Auth) sessionOK(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return false
	}
	ok, err := a.sessions.SessionValid(r.Context(), hashToken(c.Value), a.fp)
	if err != nil {
		a.log.Error("session lookup failed", "err", err)
		return false
	}
	return ok
}

type pageData struct {
	Error string
	User  string
}

func (a *Auth) loginPage(w http.ResponseWriter, r *http.Request) {
	if a.sessionOK(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	a.render(w, http.StatusOK, pageData{})
}

func (a *Auth) login(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "cross-origin request blocked", http.StatusForbidden)
		return
	}
	ip := clientIP(r)
	if a.limiter.blocked(ip) {
		a.log.Warn("login blocked by rate limit", "ip", ip)
		a.render(w, http.StatusTooManyRequests, pageData{Error: "Слишком много попыток. Отдохни 15 минут и попробуй снова."})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	user, password := r.PostFormValue("username"), r.PostFormValue("password")

	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(a.cfg.User)) == 1
	// bcrypt проверяем всегда, даже при чужом логине: время ответа не выдаёт, какой логин верный
	passOK := bcrypt.CompareHashAndPassword([]byte(a.cfg.PasswordHash), []byte(password)) == nil
	if !userOK || !passOK {
		a.limiter.fail(ip)
		a.log.Warn("login failed", "ip", ip)
		a.render(w, http.StatusUnauthorized, pageData{Error: "Неверный логин или пароль.", User: user})
		return
	}
	a.limiter.reset(ip)

	token, err := newToken()
	if err != nil {
		a.log.Error("token generation failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	expires := time.Now().Add(a.cfg.SessionTTL)
	if err := a.sessions.CreateSession(r.Context(), hashToken(token), a.fp, expires); err != nil {
		a.log.Error("session create failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, a.cookie(token, expires))
	a.log.Info("login ok", "ip", ip)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *Auth) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		if err := a.sessions.DeleteSession(r.Context(), hashToken(c.Value)); err != nil {
			a.log.Error("session delete failed", "err", err)
		}
	}
	c := a.cookie("", time.Unix(0, 0))
	c.MaxAge = -1
	http.SetCookie(w, c)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// logoutAll удаляет все сессии — например, если потерялся телефон. Доступен только после входа (Protect).
func (a *Auth) logoutAll(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "cross-origin request blocked", http.StatusForbidden)
		return
	}
	if err := a.sessions.DeleteAllSessions(r.Context()); err != nil {
		a.log.Error("delete all sessions failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	a.log.Info("logged out everywhere", "ip", clientIP(r))
	c := a.cookie("", time.Unix(0, 0))
	c.MaxAge = -1
	http.SetCookie(w, c)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *Auth) cookie(value string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     cookieName,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,                    // JavaScript не видит cookie — XSS не украдёт сессию
		Secure:   true,                    // только по https; http://localhost браузеры считают безопасным, так что локально тоже работает
		SameSite: http.SameSiteStrictMode, // cookie не уходит с запросами с чужих сайтов
	}
}

func (a *Auth) render(w http.ResponseWriter, code int, d pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := a.page.Execute(w, d); err != nil {
		a.log.Error("login page render failed", "err", err)
	}
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(t string) []byte {
	h := sha256.Sum256([]byte(t))
	return h[:]
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// sameOrigin: браузеры ставят Origin на все не-GET запросы. Нет Origin — это не браузер (curl), пропускаем.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

// clientIP: за ingress-nginx настоящий адрес приходит в X-Real-IP.
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// limiter — простая защита от перебора: после max неудач с одного IP вход закрыт на window.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	fails  map[string][]time.Time
	now    func() time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, fails: map[string][]time.Time{}, now: time.Now}
}

func (l *limiter) recent(ip string) []time.Time {
	cut := l.now().Add(-l.window)
	kept := l.fails[ip][:0]
	for _, t := range l.fails[ip] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.fails, ip)
		return nil
	}
	l.fails[ip] = kept
	return kept
}

func (l *limiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(ip)) >= l.max
}

func (l *limiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[ip] = append(l.recent(ip), l.now())
}

func (l *limiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}
