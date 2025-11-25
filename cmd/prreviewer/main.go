package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"prreviewer/configs"
	"prreviewer/internal/api"
	"prreviewer/internal/service"
	"prreviewer/internal/storage"

	"go.uber.org/zap"
)

var (
	migrateFunc = storage.RunMigrations
	newStore    = storage.NewStore
)

func main() {
	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("failed to init logger: %v", err)
	}
	sugar := logger.Sugar()
	defer func() {
		if err := logger.Sync(); err != nil {
			sugar.Warnf("logger sync: %v", err)
		}
	}()

	cfg, err := configs.Load()
	if err != nil {
		sugar.Fatalf("config load failed: %v", err)
	}

	handler, cleanup, err := bootstrap(cfg, sql.Open, sugar)
	if err != nil {
		sugar.Fatalf("bootstrap failed: %v", err)
	}
	defer cleanup()

	sigCtx, sigCancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer sigCancel()

	if err := run(sigCtx, handler, sugar, cfg.HTTPAddr); err != nil {
		sugar.Fatalf("server failed: %v", err)
	}
}

func run(ctx context.Context, srv http.Handler, logger *zap.SugaredLogger, addr string) error {
	server := &http.Server{
		Addr:              addr,
		Handler:           srv,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	logger.Infof("server listening on %s", server.Addr)

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		logger.Info("shutting down...")
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Errorf("graceful shutdown failed: %v", err)
			return err
		}
		<-errCh
		logger.Info("server stopped")
		return nil
	case err := <-errCh:
		if err != nil {
			return err
		}
		return nil
	}
}

func bootstrap(
	cfg *configs.Config,
	openDB func(driverName, dsn string) (*sql.DB, error),
	logger *zap.SugaredLogger,
) (http.Handler, func(), error) {
	db, err := openDB("pgx", cfg.DatabaseURL)
	if err != nil {
		return nil, func() {}, err
	}
	db.SetMaxOpenConns(50)
	db.SetMaxIdleConns(25)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := migrateFunc(ctx, db); err != nil {
		_ = db.Close()
		return nil, func() {}, err
	}

	store := newStore(db, logger)
	svc := service.New(store)
	handler := api.NewServer(svc, logger).Routes()

	cleanup := func() {
		_ = db.Close()
	}
	return handler, cleanup, nil
}
