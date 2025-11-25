package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"go.uber.org/zap"
)

const (
	teamBackend = "backend"
	authorID    = "author"
)

func newMockStore(t *testing.T) (*Store, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	mock.MatchExpectationsInOrder(false)
	store := NewStore(db, zap.NewNop().Sugar())
	return store, mock, func() { db.Close() }
}

func TestAddTeamSuccess(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO teams`).WithArgs("backend").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO users`).WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "backend").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO users`).WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "backend").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT u.user_id, u.username, u.is_active`).
		WithArgs("backend").
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "username", "is_active"}).
			AddRow("u1", "Alice", true).
			AddRow("u2", "Bob", false))
	mock.ExpectCommit()

	payload := TeamPayload{
		TeamName: "backend",
		Members: []TeamUpserted{
			{UserID: "u1", Username: "Alice", IsActive: true},
			{UserID: "u2", Username: "Bob", IsActive: false},
		},
	}
	if _, err := store.AddTeam(context.Background(), payload); err != nil {
		t.Fatalf("AddTeam returned error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestAddTeamDuplicateSkipsExtra(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO teams`).WithArgs("backend").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO users`).WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "backend").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT u.user_id, u.username, u.is_active`).
		WithArgs("backend").
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "username", "is_active"}).
			AddRow("u1", "Alice", true))
	mock.ExpectCommit()

	payload := TeamPayload{
		TeamName: "backend",
		Members: []TeamUpserted{
			{UserID: "u1", Username: "Alice", IsActive: true},
			{UserID: "u1", Username: "Alice2", IsActive: true},
		},
	}
	if _, err := store.AddTeam(context.Background(), payload); err != nil {
		t.Fatalf("AddTeam returned error: %v", err)
	}
}

func TestAddTeamExists(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO teams`).WithArgs("backend").
		WillReturnError(errors.New("duplicate key value"))
	mock.ExpectRollback()

	_, err := store.AddTeam(context.Background(), TeamPayload{TeamName: "backend"})
	if !errors.Is(err, ErrTeamExists) {
		t.Fatalf("expected ErrTeamExists, got %v", err)
	}
}

func TestStats(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	rows := sqlmock.NewRows([]string{"user_id", "cnt"}).
		AddRow("u1", 2).
		AddRow("u2", 0)
	mock.ExpectQuery(`SELECT u.user_id, COUNT`).WillReturnRows(rows)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM pull_requests WHERE status=\$1`).WithArgs(StatusOpen).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM pull_requests WHERE status=\$1`).WithArgs(StatusMerged).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))

	stats, err := store.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats returned error: %v", err)
	}
	if stats.AssignmentsPerUser["u1"] != 2 || stats.AssignmentsPerUser["u2"] != 0 {
		t.Fatalf("unexpected assignments: %+v", stats.AssignmentsPerUser)
	}
	if stats.OpenPRs != 3 || stats.MergedPRs != 5 {
		t.Fatalf("unexpected counts: %+v", stats)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestPickCandidatesHonorsExcludeAndBlock(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	rows := sqlmock.NewRows([]string{"user_id"}).
		AddRow("u1").
		AddRow("u2").
		AddRow("u3")
	mock.ExpectQuery(`SELECT user_id FROM users`).WithArgs("backend", "author").
		WillReturnRows(rows)

	const (
		teamBackend = "backend"
		authorID    = "author"
	)

	block := map[string]struct{}{"u2": {}}
	cands, err := store.pickCandidates(context.Background(), store.db, teamBackend, authorID, block, 2)
	if err != nil {
		t.Fatalf("pickCandidates error: %v", err)
	}
	for _, c := range cands {
		if c == authorID || c == "u2" {
			t.Fatalf("candidate not expected: %s", c)
		}
	}
	if len(cands) == 0 || len(cands) > 2 {
		t.Fatalf("unexpected candidates len: %d", len(cands))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestCreatePRAssignsReviewers(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM pull_requests WHERE pr_id=`).
		WithArgs("pr1").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery(`SELECT user_id, team_name FROM users WHERE user_id=`).
		WithArgs(authorID).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "team_name"}).AddRow(authorID, teamBackend))
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM teams WHERE name=`).
		WithArgs(teamBackend).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(`SELECT user_id FROM users`).
		WithArgs(teamBackend, authorID).
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow("u1").AddRow("u2"))
	mock.ExpectExec(`INSERT INTO pull_requests`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO assigned_reviewers`).WithArgs("pr1", "u1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO assigned_reviewers`).WithArgs("pr1", "u2").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	pr, err := store.CreatePR(context.Background(), CreatePRPayload{
		ID:     "pr1",
		Name:   "feature",
		Author: "author",
	})
	if err != nil {
		t.Fatalf("CreatePR error: %v", err)
	}
	if pr.Status != StatusOpen || len(pr.AssignedReviewers) != 2 {
		t.Fatalf("unexpected pr: %+v", pr)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestMergePRIdempotent(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectQuery(`UPDATE pull_requests`).
		WithArgs("pr1", StatusMerged).
		WillReturnRows(sqlmock.NewRows([]string{"pr_id", "pr_name", "author_id", "status", "created_at", "merged_at"}).
			AddRow("pr1", "feature", "author", StatusMerged, time.Now(), time.Now()))
	mock.ExpectQuery(`SELECT user_id FROM assigned_reviewers`).WithArgs("pr1").
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow("u1").AddRow("u2"))

	pr, err := store.MergePR(context.Background(), "pr1")
	if err != nil {
		t.Fatalf("MergePR error: %v", err)
	}
	if pr.Status != StatusMerged || len(pr.AssignedReviewers) != 2 {
		t.Fatalf("unexpected pr: %+v", pr)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestReassignHappyPath(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT author_id, status FROM pull_requests`).WithArgs("pr1").
		WillReturnRows(sqlmock.NewRows([]string{"author_id", "status"}).AddRow("author", StatusOpen))
	mock.ExpectQuery(`SELECT user_id FROM assigned_reviewers`).WithArgs("pr1").
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow("old").AddRow("other"))
	mock.ExpectQuery(`SELECT team_name FROM users`).WithArgs("old").
		WillReturnRows(sqlmock.NewRows([]string{"team_name"}).AddRow("backend"))
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM assigned_reviewers`).WithArgs("pr1", "old").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(`SELECT user_id FROM users`).WithArgs("backend", "").
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow("cand"))
	mock.ExpectExec(`DELETE FROM assigned_reviewers`).WithArgs("pr1", "old").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO assigned_reviewers`).WithArgs("pr1", "cand").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT pr_id, pr_name, author_id, status, created_at, merged_at FROM pull_requests`).
		WithArgs("pr1").
		WillReturnRows(sqlmock.NewRows([]string{"pr_id", "pr_name", "author_id", "status", "created_at", "merged_at"}).
			AddRow("pr1", "feature", "author", StatusOpen, time.Now(), time.Now()))
	mock.ExpectQuery(`SELECT user_id FROM assigned_reviewers`).WithArgs("pr1").
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow("cand").AddRow("other"))
	mock.ExpectCommit()

	pr, replaced, err := store.Reassign(context.Background(), ReassignPayload{PRID: "pr1", Old: "old"})
	if err != nil {
		t.Fatalf("Reassign error: %v", err)
	}
	if replaced != "cand" || len(pr.AssignedReviewers) != 2 {
		t.Fatalf("unexpected result: %+v, replaced=%s", pr, replaced)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestMassDeactivateNoCandidates(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM teams WHERE name=`).
		WithArgs("backend").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec(`UPDATE users SET is_active=false WHERE team_name=`).
		WithArgs("backend").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectQuery(`SELECT pr.pr_id, pr.author_id, ar.user_id`).
		WithArgs(StatusOpen, "backend").
		WillReturnRows(sqlmock.NewRows([]string{"pr_id", "author_id", "user_id"}).
			AddRow("pr1", "author", "rev1"))
	mock.ExpectQuery(`SELECT user_id FROM assigned_reviewers WHERE pr_id=`).
		WithArgs("pr1").
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow("rev1"))
	mock.ExpectQuery(`SELECT user_id FROM users`).WithArgs("backend", "").
		WillReturnRows(sqlmock.NewRows([]string{"user_id"})) // no candidates
	mock.ExpectExec(`DELETE FROM assigned_reviewers`).WithArgs("pr1", "rev1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := store.MassDeactivate(context.Background(), "backend"); err != nil {
		t.Fatalf("MassDeactivate error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestMassDeactivateWithReplacement(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM teams WHERE name=`).
		WithArgs("backend").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec(`UPDATE users SET is_active=false WHERE team_name=`).
		WithArgs("backend").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectQuery(`SELECT pr.pr_id, pr.author_id, ar.user_id`).
		WithArgs(StatusOpen, "backend").
		WillReturnRows(sqlmock.NewRows([]string{"pr_id", "author_id", "user_id"}).
			AddRow("pr1", "author", "rev1"))
	mock.ExpectQuery(`SELECT user_id FROM assigned_reviewers WHERE pr_id=`).
		WithArgs("pr1").
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow("rev1"))
	mock.ExpectQuery(`SELECT user_id FROM users`).WithArgs("backend", "").
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow("cand1"))
	mock.ExpectExec(`DELETE FROM assigned_reviewers`).WithArgs("pr1", "rev1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO assigned_reviewers`).WithArgs("pr1", "cand1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := store.MassDeactivate(context.Background(), "backend"); err != nil {
		t.Fatalf("MassDeactivate error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestCreatePRDuplicate(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM pull_requests WHERE pr_id=`).
		WithArgs("pr1").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectRollback()

	_, err := store.CreatePR(context.Background(), CreatePRPayload{ID: "pr1"})
	if !errors.Is(err, ErrPRExists) {
		t.Fatalf("expected ErrPRExists, got %v", err)
	}
}

func TestCreatePRUserNotFound(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM pull_requests WHERE pr_id=`).
		WithArgs("pr1").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery(`SELECT user_id, team_name FROM users WHERE user_id=`).
		WithArgs("author").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	_, err := store.CreatePR(context.Background(), CreatePRPayload{ID: "pr1", Author: "author"})
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestCreatePRTeamMissing(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM pull_requests WHERE pr_id=`).
		WithArgs("pr1").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery(`SELECT user_id, team_name FROM users WHERE user_id=`).
		WithArgs("author").
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "team_name"}).AddRow("author", "backend"))
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM teams WHERE name=`).
		WithArgs("backend").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectRollback()

	_, err := store.CreatePR(context.Background(), CreatePRPayload{ID: "pr1", Author: "author"})
	if !errors.Is(err, ErrTeamNotFound) {
		t.Fatalf("expected ErrTeamNotFound, got %v", err)
	}
}

func TestReassignMerged(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT author_id, status FROM pull_requests`).WithArgs("pr1").
		WillReturnRows(sqlmock.NewRows([]string{"author_id", "status"}).AddRow("author", StatusMerged))
	mock.ExpectRollback()

	_, _, err := store.Reassign(context.Background(), ReassignPayload{PRID: "pr1", Old: "old"})
	if !errors.Is(err, ErrPRMerged) {
		t.Fatalf("expected ErrPRMerged, got %v", err)
	}
}

func TestReassignNotAssigned(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT author_id, status FROM pull_requests`).WithArgs("pr1").
		WillReturnRows(sqlmock.NewRows([]string{"author_id", "status"}).AddRow("author", StatusOpen))
	mock.ExpectQuery(`SELECT user_id FROM assigned_reviewers`).WithArgs("pr1").
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}))
	mock.ExpectQuery(`SELECT team_name FROM users`).WithArgs("old").
		WillReturnRows(sqlmock.NewRows([]string{"team_name"}).AddRow("backend"))
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM assigned_reviewers`).WithArgs("pr1", "old").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectRollback()

	_, _, err := store.Reassign(context.Background(), ReassignPayload{PRID: "pr1", Old: "old"})
	if !errors.Is(err, ErrNotAssigned) {
		t.Fatalf("expected ErrNotAssigned, got %v", err)
	}
}

func TestReassignNoCandidate(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT author_id, status FROM pull_requests`).WithArgs("pr1").
		WillReturnRows(sqlmock.NewRows([]string{"author_id", "status"}).AddRow("author", StatusOpen))
	mock.ExpectQuery(`SELECT user_id FROM assigned_reviewers`).WithArgs("pr1").
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow("old"))
	mock.ExpectQuery(`SELECT team_name FROM users`).WithArgs("old").
		WillReturnRows(sqlmock.NewRows([]string{"team_name"}).AddRow("backend"))
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM assigned_reviewers`).WithArgs("pr1", "old").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(`SELECT user_id FROM users`).WithArgs("backend", "").
		WillReturnRows(sqlmock.NewRows([]string{"user_id"})) // empty
	mock.ExpectRollback()

	_, _, err := store.Reassign(context.Background(), ReassignPayload{PRID: "pr1", Old: "old"})
	if !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("expected ErrNoCandidate, got %v", err)
	}
}

func TestUserReviewsUserMissing(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM users WHERE user_id=`).
		WithArgs("u404").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	_, err := store.UserReviews(context.Background(), "u404")
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestUserReviewsSuccess(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM users WHERE user_id=`).
		WithArgs("u1").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(`SELECT pr.pr_id, pr.pr_name, pr.author_id, pr.status`).
		WithArgs("u1").
		WillReturnRows(sqlmock.NewRows([]string{"pr_id", "pr_name", "author_id", "status"}).
			AddRow("pr1", "feature", "u2", StatusOpen).
			AddRow("pr2", "bugfix", "u3", StatusMerged))

	prs, err := store.UserReviews(context.Background(), "u1")
	if err != nil {
		t.Fatalf("UserReviews error: %v", err)
	}
	if len(prs) != 2 || prs[0].ID != "pr1" || prs[1].ID != "pr2" {
		t.Fatalf("unexpected prs: %+v", prs)
	}
}

func TestSetUserActiveSuccess(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectQuery(`UPDATE users`).
		WithArgs("u1", true).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "username", "team_name", "is_active"}).
			AddRow("u1", "alice", "backend", true))

	u, err := store.SetUserActive(context.Background(), SetActivePayload{UserID: "u1", IsActive: true})
	if err != nil {
		t.Fatalf("SetUserActive error: %v", err)
	}
	if !u.IsActive || u.TeamName != teamBackend || u.ID != "u1" {
		t.Fatalf("unexpected user: %+v", u)
	}
}

func TestSetUserActiveNotFound(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectQuery(`UPDATE users`).
		WithArgs("u404", true).
		WillReturnError(sql.ErrNoRows)

	_, err := store.SetUserActive(context.Background(), SetActivePayload{UserID: "u404", IsActive: true})
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestGetTeamSuccess(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT u.user_id, u.username, u.is_active`).
		WithArgs("backend").
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "username", "is_active"}).
			AddRow("u1", "alice", true).
			AddRow("u2", "bob", false))

	team, err := store.GetTeam(context.Background(), "backend")
	if err != nil {
		t.Fatalf("GetTeam error: %v", err)
	}
	if len(team.Members) != 2 || team.TeamName != teamBackend {
		t.Fatalf("unexpected team: %+v", team)
	}
}

func TestGetTeamNotFound(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT u.user_id, u.username, u.is_active`).
		WithArgs("unknown").
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "username", "is_active"}))
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM teams WHERE name=`).
		WithArgs("unknown").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	_, err := store.GetTeam(context.Background(), "unknown")
	if !errors.Is(err, ErrTeamNotFound) {
		t.Fatalf("expected ErrTeamNotFound, got %v", err)
	}
}

func TestGetTeamExistsButEmpty(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT u.user_id, u.username, u.is_active`).
		WithArgs("empty").
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "username", "is_active"}))
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM teams WHERE name=`).
		WithArgs("empty").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	team, err := store.GetTeam(context.Background(), "empty")
	if err != nil {
		t.Fatalf("GetTeam error: %v", err)
	}
	if len(team.Members) != 0 {
		t.Fatalf("expected empty members, got %v", len(team.Members))
	}
}

func TestPickCandidatesTrimsLimit(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	rows := sqlmock.NewRows([]string{"user_id"}).
		AddRow("u1").
		AddRow("u2").
		AddRow("u3")
	mock.ExpectQuery(`SELECT user_id FROM users`).WithArgs("backend", "").
		WillReturnRows(rows)

	cands, err := store.pickCandidates(context.Background(), store.db, "backend", "", nil, 2)
	if err != nil {
		t.Fatalf("pickCandidates error: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(cands))
	}
}

func TestFetchPRTxNotFound(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT pr_id, pr_name, author_id, status, created_at, merged_at FROM pull_requests`).
		WithArgs("pr404").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	tx, err := store.db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := store.fetchPRTx(context.Background(), tx, "pr404"); !errors.Is(err, ErrPRNotFound) {
		t.Fatalf("expected ErrPRNotFound, got %v", err)
	}
	_ = tx.Rollback()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRunMigrations(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectExec(`(?s).*`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`(?s).*`).WillReturnResult(sqlmock.NewResult(0, 0))

	if err := RunMigrations(context.Background(), db); err != nil {
		t.Fatalf("RunMigrations error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestMassDeactivateTeamNotFound(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM teams WHERE name=`).
		WithArgs("unknown").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectRollback()

	err := store.MassDeactivate(context.Background(), "unknown")
	if !errors.Is(err, ErrTeamNotFound) {
		t.Fatalf("expected ErrTeamNotFound, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestMergePRNotFound(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectQuery(`UPDATE pull_requests`).
		WithArgs("pr404", StatusMerged).
		WillReturnError(sql.ErrNoRows)

	_, err := store.MergePR(context.Background(), "pr404")
	if !errors.Is(err, ErrPRNotFound) {
		t.Fatalf("expected ErrPRNotFound, got %v", err)
	}
}

func TestCreatePRPickCandidatesError(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM pull_requests WHERE pr_id=`).
		WithArgs("pr1").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery(`SELECT user_id, team_name FROM users WHERE user_id=`).
		WithArgs("author").
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "team_name"}).AddRow("author", "backend"))
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM teams WHERE name=`).
		WithArgs("backend").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(`SELECT user_id FROM users`).WithArgs("backend", "author").
		WillReturnError(errors.New("db error"))
	mock.ExpectRollback()

	if _, err := store.CreatePR(context.Background(), CreatePRPayload{ID: "pr1", Author: "author"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestReassignUserNotFound(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT author_id, status FROM pull_requests`).WithArgs("pr1").
		WillReturnRows(sqlmock.NewRows([]string{"author_id", "status"}).AddRow("author", StatusOpen))
	mock.ExpectQuery(`SELECT user_id FROM assigned_reviewers`).WithArgs("pr1").
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow("old"))
	mock.ExpectQuery(`SELECT team_name FROM users`).WithArgs("old").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	_, _, err := store.Reassign(context.Background(), ReassignPayload{PRID: "pr1", Old: "old"})
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestLoadPRMetaMerged(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT author_id, status FROM pull_requests`).WithArgs("pr1").
		WillReturnRows(sqlmock.NewRows([]string{"author_id", "status"}).AddRow("author", StatusMerged))
	mock.ExpectRollback()

	tx, _ := store.db.Begin()
	if _, err := store.loadPRMeta(context.Background(), tx, "pr1"); !errors.Is(err, ErrPRMerged) {
		t.Fatalf("expected ErrPRMerged, got %v", err)
	}
	_ = tx.Rollback()
}

func TestLookupReviewerTeamNotFound(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT team_name FROM users`).WithArgs("u404").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	tx, _ := store.db.Begin()
	if _, err := store.lookupReviewerTeam(context.Background(), tx, "u404"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
	_ = tx.Rollback()
}

func TestListReviewersTxError(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	rows := sqlmock.NewRows([]string{"user_id"}).
		AddRow("u1").
		AddRow("u2").
		RowError(1, errors.New("scan error"))
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT user_id FROM assigned_reviewers`).WithArgs("pr1").
		WillReturnRows(rows)
	mock.ExpectRollback()

	tx, _ := store.db.Begin()
	if _, err := store.listReviewersTx(context.Background(), tx, "pr1"); err == nil {
		t.Fatal("expected error")
	}
	_ = tx.Rollback()
}

func TestLoadPRMetaSuccess(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT author_id, status FROM pull_requests`).WithArgs("pr1").
		WillReturnRows(sqlmock.NewRows([]string{"author_id", "status"}).AddRow("author", StatusOpen))
	mock.ExpectQuery(`SELECT user_id FROM assigned_reviewers`).WithArgs("pr1").
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow("u1").AddRow("u2"))
	mock.ExpectRollback()

	tx, _ := store.db.Begin()
	meta, err := store.loadPRMeta(context.Background(), tx, "pr1")
	if err != nil {
		t.Fatalf("loadPRMeta error: %v", err)
	}
	if meta.authorID != "author" || len(meta.currentReviewers) != 2 {
		t.Fatalf("unexpected meta: %+v", meta)
	}
	_ = tx.Rollback()
}

func TestLookupReviewerTeamSuccess(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT team_name FROM users`).WithArgs("u1").
		WillReturnRows(sqlmock.NewRows([]string{"team_name"}).AddRow("backend"))
	mock.ExpectRollback()

	tx, _ := store.db.Begin()
	team, err := store.lookupReviewerTeam(context.Background(), tx, "u1")
	if err != nil {
		t.Fatalf("lookupReviewerTeam error: %v", err)
	}
	if team != "backend" {
		t.Fatalf("unexpected team: %s", team)
	}
	_ = tx.Rollback()
}

func TestEnsureReviewerAssignedFalse(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM assigned_reviewers`).WithArgs("pr1", "u1").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectRollback()

	tx, _ := store.db.Begin()
	err := store.ensureReviewerAssigned(context.Background(), tx, ReassignPayload{PRID: "pr1", Old: "u1"})
	if !errors.Is(err, ErrNotAssigned) {
		t.Fatalf("expected ErrNotAssigned, got %v", err)
	}
	_ = tx.Rollback()
}

func TestFetchTeamAssignmentsError(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT pr.pr_id, pr.author_id, ar.user_id`).
		WithArgs(StatusOpen, "backend").
		WillReturnError(errors.New("query error"))
	mock.ExpectRollback()

	tx, _ := store.db.Begin()
	if _, err := store.fetchTeamAssignments(context.Background(), tx, "backend"); err == nil {
		t.Fatal("expected error")
	}
	_ = tx.Rollback()
}

func TestBuildTeamQueryError(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT u.user_id, u.username, u.is_active`).
		WithArgs("backend").
		WillReturnError(errors.New("query failed"))

	if _, err := store.GetTeam(context.Background(), "backend"); err == nil {
		t.Fatal("expected error")
	}
}

func TestReassignPRNotFound(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT author_id, status FROM pull_requests`).WithArgs("pr404").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	_, _, err := store.Reassign(context.Background(), ReassignPayload{PRID: "pr404", Old: "old"})
	if !errors.Is(err, ErrPRNotFound) {
		t.Fatalf("expected ErrPRNotFound, got %v", err)
	}
}

func TestListReviewersError(t *testing.T) {
	store, mock, cleanup := newMockStore(t)
	defer cleanup()

	rows := sqlmock.NewRows([]string{"user_id"}).
		AddRow("u1").
		AddRow("u2").
		RowError(1, errors.New("scan error"))
	mock.ExpectQuery(`SELECT user_id FROM assigned_reviewers`).WithArgs("pr1").
		WillReturnRows(rows)

	if _, err := store.listReviewers(context.Background(), "pr1"); err == nil {
		t.Fatal("expected error")
	}
}
