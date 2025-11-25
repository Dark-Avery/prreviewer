package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"prreviewer/internal/service"
	"prreviewer/internal/storage"

	"go.uber.org/zap/zaptest"
)

type stubStore struct {
	createPR    func(ctx context.Context, payload storage.CreatePRPayload) (*storage.PullRequest, error)
	reassign    func(ctx context.Context, payload storage.ReassignPayload) (*storage.PullRequest, string, error)
	userReviews func(ctx context.Context, userID string) ([]storage.PullRequestShort, error)
	stats       func(ctx context.Context) (*storage.Stats, error)
	addTeam     func(ctx context.Context, payload storage.TeamPayload) (storage.TeamPayload, error)
	getTeam     func(ctx context.Context, teamName string) (storage.TeamPayload, error)
	setIsActive func(ctx context.Context, payload storage.SetActivePayload) (*storage.User, error)
	merge       func(ctx context.Context, id string) (*storage.PullRequest, error)
	deactivate  func(ctx context.Context, team string) error
}

func (s *stubStore) AddTeam(_ context.Context, payload storage.TeamPayload) (storage.TeamPayload, error) {
	if s.addTeam != nil {
		return s.addTeam(context.Background(), payload)
	}
	return payload, nil
}

func (s *stubStore) GetTeam(_ context.Context, teamName string) (storage.TeamPayload, error) {
	if s.getTeam != nil {
		return s.getTeam(context.Background(), teamName)
	}
	return storage.TeamPayload{TeamName: teamName}, nil
}

func (s *stubStore) SetUserActive(_ context.Context, payload storage.SetActivePayload) (*storage.User, error) {
	if s.setIsActive != nil {
		return s.setIsActive(context.Background(), payload)
	}
	return &storage.User{ID: payload.UserID, IsActive: payload.IsActive}, nil
}

func (s *stubStore) CreatePR(ctx context.Context, payload storage.CreatePRPayload) (*storage.PullRequest, error) {
	if s.createPR != nil {
		return s.createPR(ctx, payload)
	}
	return &storage.PullRequest{ID: payload.ID, AuthorID: payload.Author}, nil
}

func (s *stubStore) MergePR(_ context.Context, id string) (*storage.PullRequest, error) {
	if s.merge != nil {
		return s.merge(context.Background(), id)
	}
	return &storage.PullRequest{ID: id, Status: storage.StatusMerged}, nil
}

func (s *stubStore) Reassign(ctx context.Context, payload storage.ReassignPayload) (*storage.PullRequest, string, error) {
	if s.reassign != nil {
		return s.reassign(ctx, payload)
	}
	return &storage.PullRequest{ID: payload.PRID}, "u2", nil
}

func (s *stubStore) UserReviews(_ context.Context, userID string) ([]storage.PullRequestShort, error) {
	if s.userReviews != nil {
		return s.userReviews(context.Background(), userID)
	}
	return []storage.PullRequestShort{{ID: "pr1", AuthorID: userID}}, nil
}

func (s *stubStore) Stats(context.Context) (*storage.Stats, error) {
	if s.stats != nil {
		return s.stats(context.Background())
	}
	return &storage.Stats{AssignmentsPerUser: map[string]int{"u1": 1}}, nil
}

func (s *stubStore) MassDeactivate(context.Context, string) error {
	if s.deactivate != nil {
		return s.deactivate(context.Background(), "")
	}
	return nil
}

func newTestServer(t *testing.T, store service.Store) *server {
	t.Helper()
	logger := zaptest.NewLogger(t).Sugar()
	return NewServer(service.New(store), logger)
}

func newJSONRequest(t *testing.T, method, url, body string) *http.Request {
	t.Helper()
	var buf *bytes.Buffer
	if body != "" {
		buf = bytes.NewBufferString(body)
	} else {
		buf = bytes.NewBuffer(nil)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func TestHandleCreatePRValidation(t *testing.T) {
	srv := newTestServer(t, &stubStore{})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	req := newJSONRequest(t, http.MethodPost, ts.URL+"/pullRequest/create", `{}`)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Error.Code != "BAD_REQUEST" {
		t.Fatalf("unexpected code: %s", out.Error.Code)
	}
}

func TestHandleReassignNoCandidate(t *testing.T) {
	srv := newTestServer(t, &stubStore{
		reassign: func(context.Context, storage.ReassignPayload) (*storage.PullRequest, string, error) {
			return nil, "", storage.ErrNoCandidate
		},
	})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	body := bytes.NewBufferString(`{"pull_request_id":"pr1","old_user_id":"u1"}`)
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		ts.URL+"/pullRequest/reassign",
		body,
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", resp.StatusCode)
	}
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Error.Code != "NO_CANDIDATE" {
		t.Fatalf("unexpected code: %s", out.Error.Code)
	}
}

func TestMapErrorWithLog(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	cases := []struct {
		err      error
		wantCode string
		status   int
	}{
		{storage.ErrTeamExists, "TEAM_EXISTS", http.StatusBadRequest},
		{storage.ErrPRExists, "PR_EXISTS", http.StatusConflict},
		{storage.ErrPRMerged, "PR_MERGED", http.StatusConflict},
		{storage.ErrNotAssigned, "NOT_ASSIGNED", http.StatusConflict},
		{storage.ErrNoCandidate, "NO_CANDIDATE", http.StatusConflict},
		{storage.ErrUserNotFound, "NOT_FOUND", http.StatusNotFound},
		{errors.New("boom"), "INTERNAL", http.StatusInternalServerError},
	}
	for _, tc := range cases {
		apiErr := mapErrorWithLog(logger, tc.err)
		if apiErr.Code != tc.wantCode {
			t.Fatalf("code = %s, want %s", apiErr.Code, tc.wantCode)
		}
		if apiErr.HTTPStatus != tc.status {
			t.Fatalf("status = %d, want %d", apiErr.HTTPStatus, tc.status)
		}
	}
}

func TestHandleSetIsActiveSuccess(t *testing.T) {
	srv := newTestServer(t, &stubStore{})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	req := newJSONRequest(t, http.MethodPost, ts.URL+"/users/setIsActive", `{"user_id":"u1","is_active":true}`)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestHandleGetReviewSuccess(t *testing.T) {
	srv := newTestServer(t, &stubStore{})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		ts.URL+"/users/getReview?user_id=u1",
		http.NoBody,
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestHandleCreatePRSuccess(t *testing.T) {
	srv := newTestServer(t, &stubStore{})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	body := bytes.NewBufferString(`{"pull_request_id":"pr1","pull_request_name":"Feature","author_id":"u1"}`)
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		ts.URL+"/pullRequest/create",
		body,
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestHandleCreatePRExists(t *testing.T) {
	srv := newTestServer(t, &stubStore{
		createPR: func(ctx context.Context, payload storage.CreatePRPayload) (*storage.PullRequest, error) {
			return nil, storage.ErrPRExists
		},
	})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	req := newJSONRequest(t, http.MethodPost, ts.URL+"/pullRequest/create", `{"pull_request_id":"pr1","pull_request_name":"Feature","author_id":"u1"}`)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestHandleMergePRSuccess(t *testing.T) {
	srv := newTestServer(t, &stubStore{})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	body := bytes.NewBufferString(`{"pull_request_id":"pr1"}`)
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		ts.URL+"/pullRequest/merge",
		body,
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestHandleMergePRNotFound(t *testing.T) {
	srv := newTestServer(t, &stubStore{
		merge: func(ctx context.Context, id string) (*storage.PullRequest, error) {
			return nil, storage.ErrPRNotFound
		},
	})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	req := newJSONRequest(t, http.MethodPost, ts.URL+"/pullRequest/merge", `{"pull_request_id":"pr404"}`)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestHandleReassignSuccess(t *testing.T) {
	srv := newTestServer(t, &stubStore{})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	req := newJSONRequest(t, http.MethodPost, ts.URL+"/pullRequest/reassign", `{"pull_request_id":"pr1","old_user_id":"u1"}`)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestHandleReassignErrors(t *testing.T) {
	cases := []struct {
		err         error
		wantStatus  int
		description string
	}{
		{storage.ErrPRMerged, http.StatusConflict, "merged"},
		{storage.ErrNotAssigned, http.StatusConflict, "not assigned"},
		{storage.ErrNoCandidate, http.StatusConflict, "no candidate"},
		{storage.ErrPRNotFound, http.StatusNotFound, "pr not found"},
		{storage.ErrUserNotFound, http.StatusNotFound, "user not found"},
		{storage.ErrTeamNotFound, http.StatusNotFound, "team not found"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.description, func(t *testing.T) {
			srv := newTestServer(t, &stubStore{
				reassign: func(ctx context.Context, payload storage.ReassignPayload) (*storage.PullRequest, string, error) {
					return nil, "", tc.err
				},
			})
			ts := httptest.NewServer(srv.Routes())
			t.Cleanup(ts.Close)

			req := newJSONRequest(t, http.MethodPost, ts.URL+"/pullRequest/reassign", `{"pull_request_id":"pr1","old_user_id":"u1"}`)
			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatalf("do: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
		})
	}
}

func TestHandleStatsSuccess(t *testing.T) {
	srv := newTestServer(t, &stubStore{})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		ts.URL+"/stats",
		http.NoBody,
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestHandleAddTeamNotFound(t *testing.T) {
	srv := newTestServer(t, &stubStore{
		addTeam: func(ctx context.Context, payload storage.TeamPayload) (storage.TeamPayload, error) {
			return storage.TeamPayload{}, storage.ErrTeamNotFound
		},
	})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	body := bytes.NewBufferString(`{"team_name":"x","members":[]}`)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/team/add", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestHandleAddTeamExists(t *testing.T) {
	srv := newTestServer(t, &stubStore{
		addTeam: func(ctx context.Context, payload storage.TeamPayload) (storage.TeamPayload, error) {
			return storage.TeamPayload{}, storage.ErrTeamExists
		},
	})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	req := newJSONRequest(t, http.MethodPost, ts.URL+"/team/add", `{"team_name":"backend","members":[]}`)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestHandleAddTeamSuccess(t *testing.T) {
	srv := newTestServer(t, &stubStore{})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	body := bytes.NewBufferString(`{"team_name":"backend","members":[{"user_id":"u1","username":"alice","is_active":true}]}`)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/team/add", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestHandleDeactivateTeamSuccess(t *testing.T) {
	srv := newTestServer(t, &stubStore{})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	body := bytes.NewBufferString(`{"team_name":"backend"}`)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/team/deactivate", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestHandleGetTeamSuccess(t *testing.T) {
	srv := newTestServer(t, &stubStore{})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/team/get?team_name=backend", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestHandleGetTeamNotFound(t *testing.T) {
	srv := newTestServer(t, &stubStore{
		getTeam: func(ctx context.Context, teamName string) (storage.TeamPayload, error) {
			return storage.TeamPayload{}, storage.ErrTeamNotFound
		},
	})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		ts.URL+"/team/get?team_name=x",
		http.NoBody,
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestHandleSetActiveNotFound(t *testing.T) {
	srv := newTestServer(t, &stubStore{
		setIsActive: func(ctx context.Context, payload storage.SetActivePayload) (*storage.User, error) {
			return nil, storage.ErrUserNotFound
		},
	})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	body := bytes.NewBufferString(`{"user_id":"u404","is_active":true}`)
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		ts.URL+"/users/setIsActive",
		body,
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestHandleGetReviewNotFound(t *testing.T) {
	srv := newTestServer(t, &stubStore{
		userReviews: func(ctx context.Context, userID string) ([]storage.PullRequestShort, error) {
			return nil, storage.ErrUserNotFound
		},
	})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	req := newJSONRequest(t, http.MethodGet, ts.URL+"/users/getReview?user_id=u404", "")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestHandleStatsInternal(t *testing.T) {
	srv := newTestServer(t, &stubStore{
		stats: func(ctx context.Context) (*storage.Stats, error) {
			return nil, errors.New("boom")
		},
	})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	req := newJSONRequest(t, http.MethodGet, ts.URL+"/stats", "")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
