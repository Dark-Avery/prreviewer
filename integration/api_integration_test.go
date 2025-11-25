package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"prreviewer/internal/api"
	"prreviewer/internal/service"
	"prreviewer/internal/storage"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/zap"
)

type testEnv struct {
	store *storage.Store
	db    *sql.DB
	svc   *service.Service
}

func startPostgres(t *testing.T) (env *testEnv, cleanup func()) {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "postgres:15-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_PASSWORD": "postgres",
			"POSTGRES_USER":     "postgres",
			"POSTGRES_DB":       "testdb",
		},
		WaitingFor: wait.ForListeningPort("5432/tcp").WithStartupTimeout(30 * time.Second),
	}
	pg, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}

	host, err := pg.Host(ctx)
	if err != nil {
		t.Fatalf("host: %v", err)
	}
	port, err := pg.MappedPort(ctx, "5432")
	if err != nil {
		t.Fatalf("port: %v", err)
	}
	dsn := fmt.Sprintf("postgres://postgres:postgres@%s:%s/testdb?sslmode=disable", host, port.Port())
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)

	if err := storage.RunMigrations(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	store := storage.NewStore(db, zap.NewNop().Sugar())
	svc := service.New(store)
	cleanup = func() {
		db.Close()
		if err := pg.Terminate(context.Background()); err != nil {
			t.Logf("terminate container: %v", err)
		}
	}
	return &testEnv{store: store, db: db, svc: svc}, cleanup
}

func TestFullFlow(t *testing.T) {
	if os.Getenv("RUN_INTEGRATION") == "" {
		t.Skip("set RUN_INTEGRATION=1 to run integration tests (requires Docker)")
	}
	if testing.Short() {
		t.Skip("integration test")
	}
	env, cleanup := startPostgres(t)
	defer cleanup()

	logger, _ := zap.NewDevelopment()
	defer logger.Sync()
	s := httptest.NewServer(api.NewServer(env.svc, logger.Sugar()).Routes())
	defer s.Close()

	// add team
	body := `{"team_name":"backend","members":[{"user_id":"u1","username":"Alice","is_active":true},{"user_id":"u2","username":"Bob","is_active":true},{"user_id":"u3","username":"Charlie","is_active":true},{"user_id":"u4","username":"Dave","is_active":true}]}`
	resp := doPost(t, s, "/team/add", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add team status: %d", resp.StatusCode)
	}

	// create pr
	body = `{"pull_request_id":"pr1","pull_request_name":"Feature","author_id":"u1"}`
	resp = doPost(t, s, "/pullRequest/create", body)
	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("create pr status: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}
	var prResp struct {
		PR struct {
			AssignedReviewers []string `json:"assigned_reviewers"`
		} `json:"pr"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&prResp); err != nil {
		t.Fatalf("decode pr response: %v", err)
	}
	if len(prResp.PR.AssignedReviewers) == 0 {
		t.Fatalf("no reviewers assigned")
	}
	reviewerToReassign := prResp.PR.AssignedReviewers[0]

	body = fmt.Sprintf(`{"pull_request_id":"pr1","old_user_id":"%s"}`, reviewerToReassign)
	resp = doPost(t, s, "/pullRequest/reassign", body)
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("reassign status: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	// merge idempotent
	body = `{"pull_request_id":"pr1"}`
	firstResp := doPost(t, s, "/pullRequest/merge", body)
	_ = firstResp.Body.Close()
	resp = doPost(t, s, "/pullRequest/merge", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("merge status: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// reassign on merged should conflict
	resp = doPost(t, s, "/pullRequest/reassign", fmt.Sprintf(`{"pull_request_id":"%s","old_user_id":"%s"}`, "pr1", reviewerToReassign))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("reassign merged status: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// create second PR to keep OPEN after mass deactivate
	body = `{"pull_request_id":"pr2","pull_request_name":"Bugfix","author_id":"u2"}`
	resp = doPost(t, s, "/pullRequest/create", body)
	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("create pr2 status: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}
	_ = resp.Body.Close()

	// user reviews endpoint
	resp = doGet(t, s, "/users/getReview?user_id=u2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("user reviews status: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// mass deactivate team
	resp = doPost(t, s, "/team/deactivate", `{"team_name":"backend"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("deactivate status: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// stats after operations
	resp = doGet(t, s, "/stats")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stats status: %d", resp.StatusCode)
	}
	var statsResp struct {
		AssignmentsPerUser map[string]int `json:"assignments_per_user"`
		OpenPRs            int            `json:"open_prs"`
		MergedPRs          int            `json:"merged_prs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&statsResp); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if statsResp.OpenPRs == 0 || statsResp.MergedPRs == 0 {
		t.Fatalf("unexpected stats: %+v", statsResp)
	}
	_ = resp.Body.Close()
}

func doPost(t *testing.T, srv *httptest.Server, path, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		srv.URL+path,
		bytes.NewBufferString(body),
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func doGet(t *testing.T, srv *httptest.Server, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+path, http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func TestMain(m *testing.M) {
	// allow integration tests to run serially
	os.Exit(m.Run())
}
