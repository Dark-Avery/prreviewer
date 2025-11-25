package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"
	"time"

	"prreviewer/configs"
	"prreviewer/internal/api"
	"prreviewer/internal/service"
	"prreviewer/internal/storage"

	"github.com/DATA-DOG/go-sqlmock"
	"go.uber.org/zap/zaptest"
)

type fakeStore struct{}

func (fakeStore) AddTeam(context.Context, storage.TeamPayload) (storage.TeamPayload, error) {
	return storage.TeamPayload{TeamName: "team"}, nil
}
func (fakeStore) GetTeam(context.Context, string) (storage.TeamPayload, error) {
	return storage.TeamPayload{TeamName: "team"}, nil
}
func (fakeStore) SetUserActive(context.Context, storage.SetActivePayload) (*storage.User, error) {
	return &storage.User{ID: "u1"}, nil
}
func (fakeStore) CreatePR(context.Context, storage.CreatePRPayload) (*storage.PullRequest, error) {
	return &storage.PullRequest{ID: "pr1"}, nil
}
func (fakeStore) MergePR(context.Context, string) (*storage.PullRequest, error) {
	return &storage.PullRequest{ID: "pr1", Status: storage.StatusMerged}, nil
}
func (fakeStore) Reassign(context.Context, storage.ReassignPayload) (*storage.PullRequest, string, error) {
	return &storage.PullRequest{ID: "pr1"}, "u2", nil
}
func (fakeStore) UserReviews(context.Context, string) ([]storage.PullRequestShort, error) {
	return []storage.PullRequestShort{}, nil
}
func (fakeStore) Stats(context.Context) (*storage.Stats, error) {
	return &storage.Stats{}, nil
}
func (fakeStore) MassDeactivate(context.Context, string) error { return nil }

// smoke test: server starts and stops on context cancel
func TestRunStartsAndStops(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	srv := api.NewServer(service.New(fakeStore{}), logger).Routes()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := run(ctx, srv, logger, ":0"); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

// ensure server handles shutdown without panic when ListenAndServe returns ErrServerClosed
func TestRunShutdownGraceful(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	srv := http.NewServeMux() // empty handler is fine

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := run(ctx, srv, logger, ":0"); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestRunListenError(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	srv := http.NewServeMux()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := run(ctx, srv, logger, "://bad"); err == nil {
		t.Fatal("expected listen error")
	}
}

func TestBootstrapSuccess(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	mock.ExpectClose()

	origMigrate := migrateFunc
	defer func() { migrateFunc = origMigrate }()
	migrateFunc = func(context.Context, *sql.DB) error { return nil }

	open := func(driver, dsn string) (*sql.DB, error) { return db, nil }
	cfg := &configs.Config{DatabaseURL: "custom", HTTPAddr: ":0"}
	handler, cleanup, err := bootstrap(cfg, open, logger)
	if err != nil {
		t.Fatalf("bootstrap error: %v", err)
	}
	if handler == nil {
		t.Fatal("handler nil")
	}
	cleanup()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestBootstrapMigrateError(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	origMigrate := migrateFunc
	defer func() { migrateFunc = origMigrate }()
	migrateFunc = func(context.Context, *sql.DB) error { return errors.New("migrate") }

	open := func(driver, dsn string) (*sql.DB, error) { return db, nil }
	cfg := &configs.Config{DatabaseURL: "custom", HTTPAddr: ":0"}
	if _, _, err := bootstrap(cfg, open, logger); err == nil {
		t.Fatal("expected migrate error")
	}
}

func TestBootstrapOpenError(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	open := func(driver, dsn string) (*sql.DB, error) { return nil, errors.New("open") }
	cfg := &configs.Config{DatabaseURL: "bad", HTTPAddr: ":0"}
	if _, _, err := bootstrap(cfg, open, logger); err == nil {
		t.Fatal("expected open error")
	}
}
