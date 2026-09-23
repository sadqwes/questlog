// questlog — RPG-трекер подготовки к CKA, собеседованиям и не только.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/term"

	"github.com/sadqwes/questlog/internal/api"
	"github.com/sadqwes/questlog/internal/auth"
	"github.com/sadqwes/questlog/internal/game"
	"github.com/sadqwes/questlog/internal/metrics"
	"github.com/sadqwes/questlog/internal/plan"
	"github.com/sadqwes/questlog/internal/store"
	"github.com/sadqwes/questlog/web"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "hash-password" {
		if err := hashPassword(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("questlog stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	p, err := plan.Load(os.Getenv("PLAN_FILE"))
	if err != nil {
		return err
	}

	// DATABASE_URL необязателен: без него pgx читает PGHOST, PGUSER, PGPASSWORD, PGDATABASE.
	st, err := store.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer st.Close()
	if err := migrateWithRetry(ctx, st, log); err != nil {
		return err
	}

	// Без пароля сервис не стартует: лучше упасть, чем открыть прогресс всем в сети.
	authn, err := auth.New(auth.Config{
		User: os.Getenv("QUESTLOG_USER"),
		// TrimSpace: значения из секретов, созданных через --from-file, часто заканчиваются переводом строки
		PasswordHash: strings.TrimSpace(os.Getenv("QUESTLOG_PASSWORD_HASH")),
		APIToken:     strings.TrimSpace(os.Getenv("QUESTLOG_API_TOKEN")),
	}, st, log)
	if err != nil {
		return err
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	srv := api.New(p, st, web.FS(), log, reg)
	reg.MustRegister(metrics.New(func(ctx context.Context) (game.Stats, error) {
		s, err := srv.State(ctx)
		return s.Stats, err
	}, log))

	mux := http.NewServeMux()
	srv.Register(mux)
	authn.Register(mux)

	// Приложение и метрики — на разных портах: /metrics не попадает в ingress, Prometheus ходит напрямую в сервис.
	appSrv := newServer(":"+envOr("PORT", "8080"), api.SecurityHeaders(authn.Protect(mux)))
	metricsMux := http.NewServeMux()
	metricsMux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	metricsSrv := newServer(":"+envOr("METRICS_PORT", "9090"), metricsMux)

	errc := make(chan error, 2)
	for _, s := range []*http.Server{appSrv, metricsSrv} {
		go func() { errc <- s.ListenAndServe() }()
	}
	log.Info("listening", "app", appSrv.Addr, "metrics", metricsSrv.Addr, "plan", p.Title)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range []*http.Server{appSrv, metricsSrv} {
		if err := s.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	return nil
}

func newServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

// hashPassword печатает bcrypt-хеш пароля для QUESTLOG_PASSWORD_HASH.
// В терминале пароль не отображается; можно и передать через stdin.
func hashPassword() error {
	var password string
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "Пароль: ")
		p1, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		fmt.Fprint(os.Stderr, "Ещё раз: ")
		p2, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		if string(p1) != string(p2) {
			return errors.New("пароли не совпали")
		}
		password = string(p1)
	} else {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return err
		}
		password = strings.TrimRight(line, "\r\n")
	}
	if len([]rune(password)) < 10 {
		return errors.New("пароль короче 10 символов — придумай подлиннее")
	}
	h, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	fmt.Println(h)
	return nil
}

// migrateWithRetry ждёт базу до минуты: в кластере под приложения может подняться раньше Postgres.
func migrateWithRetry(ctx context.Context, st *store.Store, log *slog.Logger) error {
	var err error
	for attempt := 1; attempt <= 30; attempt++ {
		if err = st.Migrate(ctx); err == nil {
			return nil
		}
		log.Warn("database not ready, retrying", "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return err
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
