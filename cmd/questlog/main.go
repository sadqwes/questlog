// questlog — RPG-трекер подготовки к CKA, собеседованиям и не только.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/sadqwes/questlog/internal/api"
	"github.com/sadqwes/questlog/internal/game"
	"github.com/sadqwes/questlog/internal/metrics"
	"github.com/sadqwes/questlog/internal/plan"
	"github.com/sadqwes/questlog/internal/store"
	"github.com/sadqwes/questlog/web"
)

func main() {
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

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	srv := api.New(p, st, web.FS(), log, reg)
	reg.MustRegister(metrics.New(func(ctx context.Context) (game.Stats, error) {
		s, err := srv.State(ctx)
		return s.Stats, err
	}, log))

	addr := ":" + envOr("PORT", "8080")
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Routes(promhttp.HandlerFor(reg, promhttp.HandlerOpts{})),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", addr, "plan", p.Title)
		errc <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
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
