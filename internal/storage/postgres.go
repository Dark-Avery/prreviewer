package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"go.uber.org/zap"
)

const (
	StatusOpen   = "OPEN"
	StatusMerged = "MERGED"
)

var (
	ErrTeamExists    = errors.New("team already exists")
	ErrPRExists      = errors.New("pr already exists")
	ErrPRMerged      = errors.New("pr merged")
	ErrNotAssigned   = errors.New("reviewer not assigned")
	ErrNoCandidate   = errors.New("no candidate")
	ErrUserNotFound  = errors.New("user not found")
	ErrPRNotFound    = errors.New("pr not found")
	ErrTeamNotFound  = errors.New("team not found")
	ErrInvalidStatus = errors.New("invalid status")
)

type User struct {
	ID       string `json:"user_id"`
	Username string `json:"username"`
	TeamName string `json:"team_name"`
	IsActive bool   `json:"is_active"`
}

type PullRequest struct {
	ID                string     `json:"pull_request_id"`
	Name              string     `json:"pull_request_name"`
	AuthorID          string     `json:"author_id"`
	Status            string     `json:"status"`
	AssignedReviewers []string   `json:"assigned_reviewers"`
	CreatedAt         time.Time  `json:"createdAt"`
	MergedAt          *time.Time `json:"mergedAt,omitempty"`
}

type PullRequestShort struct {
	ID       string `json:"pull_request_id"`
	Name     string `json:"pull_request_name"`
	AuthorID string `json:"author_id"`
	Status   string `json:"status"`
}

type TeamPayload struct {
	TeamName string         `json:"team_name"`
	Members  []TeamUpserted `json:"members"`
}

type TeamUpserted struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	IsActive bool   `json:"is_active"`
}

type SetActivePayload struct {
	UserID   string `json:"user_id"`
	IsActive bool   `json:"is_active"`
}

type CreatePRPayload struct {
	ID     string `json:"pull_request_id"`
	Name   string `json:"pull_request_name"`
	Author string `json:"author_id"`
}

type MergePayload struct {
	ID string `json:"pull_request_id"`
}

type ReassignPayload struct {
	PRID string `json:"pull_request_id"`
	Old  string `json:"old_user_id"`
}

type Stats struct {
	AssignmentsPerUser map[string]int `json:"assignments_per_user"`
	OpenPRs            int            `json:"open_prs"`
	MergedPRs          int            `json:"merged_prs"`
}

type Store struct {
	db     *sql.DB
	rnd    *rand.Rand
	logger *zap.SugaredLogger
}

func NewStore(db *sql.DB, logger *zap.SugaredLogger) *Store {
	if logger == nil {
		logger = zap.NewNop().Sugar()
	}
	return &Store{
		db:     db,
		logger: logger,
		// pseudo-randomness is fine for reviewer selection
		rnd: rand.New(rand.NewSource(time.Now().UnixNano())), //nolint:gosec
	}
}

func (s *Store) AddTeam(ctx context.Context, payload TeamPayload) (TeamPayload, error) {
	for attempts := 0; attempts < 3; attempts++ {
		team, err := s.addTeamOnce(ctx, payload)
		if err == nil {
			return team, nil
		}
		if isRetryable(err) && attempts < 2 {
			time.Sleep(time.Duration(attempts+1) * 10 * time.Millisecond)
			continue
		}
		return TeamPayload{}, err
	}
	return TeamPayload{}, fmt.Errorf("unreachable")
}

func (s *Store) addTeamOnce(ctx context.Context, payload TeamPayload) (TeamPayload, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TeamPayload{}, err
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			s.logger.Warnf("rollback failed: %v", err)
		}
	}()

	if _, err := tx.ExecContext(ctx, `INSERT INTO teams(name) VALUES ($1)`, payload.TeamName); err != nil {
		if isUniqueViolation(err) {
			return TeamPayload{}, ErrTeamExists
		}
		return TeamPayload{}, err
	}
	unique := make(map[string]TeamUpserted)
	for _, m := range payload.Members {
		if m.UserID == "" {
			continue
		}
		unique[m.UserID] = m
	}
	for _, m := range unique {
		_, err := tx.ExecContext(ctx, `
INSERT INTO users(user_id, username, is_active, team_name)
VALUES ($1,$2,$3,$4)
ON CONFLICT (user_id) DO UPDATE
SET username = EXCLUDED.username,
    is_active = EXCLUDED.is_active,
    team_name = EXCLUDED.team_name
`, m.UserID, m.Username, m.IsActive, payload.TeamName)
		if err != nil {
			return TeamPayload{}, err
		}
	}
	team, err := buildTeam(ctx, tx, payload.TeamName)
	if err != nil {
		return TeamPayload{}, err
	}
	if err := tx.Commit(); err != nil {
		return TeamPayload{}, err
	}
	return team, nil
}

func (s *Store) GetTeam(ctx context.Context, teamName string) (TeamPayload, error) {
	return buildTeam(ctx, s.db, teamName)
}

func (s *Store) SetUserActive(ctx context.Context, payload SetActivePayload) (*User, error) {
	row := s.db.QueryRowContext(ctx, `
UPDATE users
SET is_active = $2
WHERE user_id = $1
RETURNING user_id, username, team_name, is_active
`, payload.UserID, payload.IsActive)
	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.TeamName, &u.IsActive); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &u, nil
}

func (s *Store) CreatePR(ctx context.Context, payload CreatePRPayload) (*PullRequest, error) {
	for attempts := 0; attempts < 3; attempts++ {
		pr, err := s.createPROnce(ctx, payload)
		if err == nil {
			return pr, nil
		}
		if isSerializationError(err) && attempts < 2 {
			time.Sleep(time.Duration(attempts+1) * 10 * time.Millisecond)
			continue
		}
		return nil, err
	}
	return nil, fmt.Errorf("unreachable")
}

func (s *Store) createPROnce(ctx context.Context, payload CreatePRPayload) (*PullRequest, error) {
	// For create we use default isolation to avoid excessive serialization conflicts under load.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			s.logger.Warnf("rollback failed: %v", err)
		}
	}()

	var exists bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM pull_requests WHERE pr_id=$1)`, payload.ID).
		Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrPRExists
	}

	var authorID, teamName string
	if err := tx.QueryRowContext(ctx,
		`SELECT user_id, team_name FROM users WHERE user_id=$1`, payload.Author).
		Scan(&authorID, &teamName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	var teamExists bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM teams WHERE name=$1)`, teamName).
		Scan(&teamExists); err != nil {
		return nil, err
	}
	if !teamExists {
		return nil, ErrTeamNotFound
	}

	candidates, err := s.pickCandidates(ctx, tx, teamName, payload.Author, nil, 2)
	if err != nil {
		return nil, err
	}

	// Валидация: статус должен быть только OPEN или MERGED (валидация на уровне приложения)
	// Используем константу StatusOpen для гарантии корректности
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO pull_requests(pr_id, pr_name, author_id, status, created_at)
VALUES ($1,$2,$3,$4,$5)`, payload.ID, payload.Name, payload.Author, StatusOpen, now); err != nil {
		return nil, err
	}
	for _, c := range candidates {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO assigned_reviewers(pr_id, user_id) VALUES ($1,$2)`, payload.ID, c); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &PullRequest{
		ID:                payload.ID,
		Name:              payload.Name,
		AuthorID:          payload.Author,
		Status:            StatusOpen,
		AssignedReviewers: candidates,
		CreatedAt:         now,
	}, nil
}

func (s *Store) MergePR(ctx context.Context, id string) (*PullRequest, error) {
	// Валидация: используем константу StatusMerged для гарантии корректности
	// Валидация на уровне приложения, а не БД
	row := s.db.QueryRowContext(ctx, `
UPDATE pull_requests
SET status = $2,
    merged_at = COALESCE(merged_at, NOW())
WHERE pr_id = $1
RETURNING pr_id, pr_name, author_id, status, created_at, merged_at
`, id, StatusMerged)
	var pr PullRequest
	if err := row.Scan(&pr.ID, &pr.Name, &pr.AuthorID, &pr.Status, &pr.CreatedAt, &pr.MergedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPRNotFound
		}
		return nil, err
	}
	reviewers, err := s.listReviewers(ctx, pr.ID)
	if err != nil {
		return nil, err
	}
	pr.AssignedReviewers = reviewers
	return &pr, nil
}

func (s *Store) Reassign(ctx context.Context, payload ReassignPayload) (*PullRequest, string, error) {
	for attempts := 0; attempts < 3; attempts++ {
		pr, replacement, err := s.reassignOnce(ctx, payload)
		if err == nil {
			return pr, replacement, nil
		}
		if isSerializationError(err) && attempts < 2 {
			time.Sleep(time.Duration(attempts+1) * 10 * time.Millisecond)
			continue
		}
		return nil, "", err
	}
	return nil, "", fmt.Errorf("unreachable")
}

func (s *Store) reassignOnce(ctx context.Context, payload ReassignPayload) (*PullRequest, string, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, "", err
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			s.logger.Warnf("rollback failed: %v", err)
		}
	}()

	prMeta, err := s.loadPRMeta(ctx, tx, payload.PRID)
	if err != nil {
		return nil, "", err
	}

	reviewerTeam, err := s.lookupReviewerTeam(ctx, tx, payload.Old)
	if err != nil {
		return nil, "", err
	}

	if err := s.ensureReviewerAssigned(ctx, tx, payload); err != nil {
		return nil, "", err
	}

	block := s.buildBlocklist(payload, prMeta.currentReviewers, prMeta.authorID)
	candidates, err := s.pickCandidates(ctx, tx, reviewerTeam, "", block, 1)
	if err != nil {
		return nil, "", err
	}
	if len(candidates) == 0 {
		return nil, "", ErrNoCandidate
	}
	replacement := candidates[0]
	if err := s.replaceReviewer(ctx, tx, payload.PRID, payload.Old, replacement); err != nil {
		return nil, "", err
	}
	updated, err := s.fetchPRTx(ctx, tx, payload.PRID)
	if err != nil {
		return nil, "", err
	}
	if err := tx.Commit(); err != nil {
		return nil, "", err
	}
	return updated, replacement, nil
}

type prMetaInfo struct {
	authorID         string
	currentReviewers []string
}

func (s *Store) loadPRMeta(ctx context.Context, tx *sql.Tx, prID string) (*prMetaInfo, error) {
	var meta prMetaInfo
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT author_id, status FROM pull_requests WHERE pr_id=$1 FOR UPDATE`, prID).
		Scan(&meta.authorID, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPRNotFound
		}
		return nil, err
	}
	if status == StatusMerged {
		return nil, ErrPRMerged
	}
	reviewers, err := s.listReviewersTx(ctx, tx, prID)
	if err != nil {
		return nil, err
	}
	meta.currentReviewers = reviewers
	return &meta, nil
}

func (s *Store) lookupReviewerTeam(ctx context.Context, tx *sql.Tx, userID string) (string, error) {
	var reviewerTeam string
	if err := tx.QueryRowContext(
		ctx,
		`SELECT team_name FROM users WHERE user_id=$1`,
		userID,
	).Scan(&reviewerTeam); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrUserNotFound
		}
		return "", err
	}
	return reviewerTeam, nil
}

func (s *Store) ensureReviewerAssigned(ctx context.Context, tx *sql.Tx, payload ReassignPayload) error {
	var assigned bool
	if err := tx.QueryRowContext(
		ctx,
		`SELECT EXISTS(SELECT 1 FROM assigned_reviewers WHERE pr_id=$1 AND user_id=$2)`,
		payload.PRID,
		payload.Old,
	).Scan(&assigned); err != nil {
		return err
	}
	if !assigned {
		return ErrNotAssigned
	}
	return nil
}

func (s *Store) buildBlocklist(
	payload ReassignPayload,
	currentReviewers []string,
	authorID string,
) map[string]struct{} {
	block := make(map[string]struct{}, len(currentReviewers)+2)
	for _, r := range currentReviewers {
		block[r] = struct{}{}
	}
	block[payload.Old] = struct{}{}
	block[authorID] = struct{}{}
	return block
}

func (s *Store) replaceReviewer(ctx context.Context, tx *sql.Tx, prID, oldID, newID string) error {
	if _, err := tx.ExecContext(
		ctx,
		`DELETE FROM assigned_reviewers WHERE pr_id=$1 AND user_id=$2`,
		prID,
		oldID,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO assigned_reviewers(pr_id, user_id) VALUES ($1,$2)`,
		prID,
		newID,
	); err != nil {
		return err
	}
	return nil
}

func (s *Store) UserReviews(ctx context.Context, userID string) ([]PullRequestShort, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE user_id=$1)`, userID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrUserNotFound
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT pr.pr_id, pr.pr_name, pr.author_id, pr.status
FROM pull_requests pr
JOIN assigned_reviewers ar ON ar.pr_id = pr.pr_id
WHERE ar.user_id = $1
ORDER BY pr.pr_id
`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var prs []PullRequestShort
	for rows.Next() {
		var pr PullRequestShort
		if err := rows.Scan(&pr.ID, &pr.Name, &pr.AuthorID, &pr.Status); err != nil {
			return nil, err
		}
		prs = append(prs, pr)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return prs, nil
}

func (s *Store) Stats(ctx context.Context) (*Stats, error) {
	stats := &Stats{AssignmentsPerUser: make(map[string]int)}
	rows, err := s.db.QueryContext(ctx, `
SELECT u.user_id, COUNT(ar.pr_id) AS cnt
FROM users u
LEFT JOIN assigned_reviewers ar ON ar.user_id = u.user_id
GROUP BY u.user_id
`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var userID string
		var cnt int
		if err := rows.Scan(&userID, &cnt); err != nil {
			return nil, err
		}
		stats.AssignmentsPerUser[userID] = cnt
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM pull_requests WHERE status=$1`,
		StatusOpen,
	).Scan(&stats.OpenPRs); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM pull_requests WHERE status=$1`,
		StatusMerged,
	).Scan(&stats.MergedPRs); err != nil {
		return nil, err
	}
	return stats, nil
}

func (s *Store) MassDeactivate(ctx context.Context, teamName string) error {
	for attempts := 0; attempts < 3; attempts++ {
		err := s.massDeactivateOnce(ctx, teamName)
		if err == nil {
			return nil
		}
		if isSerializationError(err) && attempts < 2 {
			time.Sleep(time.Duration(attempts+1) * 10 * time.Millisecond)
			continue
		}
		return err
	}
	return fmt.Errorf("unreachable")
}

func (s *Store) massDeactivateOnce(ctx context.Context, teamName string) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			s.logger.Warnf("rollback failed: %v", err)
		}
	}()

	if err := s.ensureTeamExistsTx(ctx, tx, teamName); err != nil {
		return err
	}
	if err := s.deactivateUsersTx(ctx, tx, teamName); err != nil {
		return err
	}
	assignments, err := s.fetchTeamAssignments(ctx, tx, teamName)
	if err != nil {
		return err
	}
	if err := s.reassignAfterDeactivation(ctx, tx, teamName, assignments); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ensureTeamExistsTx(ctx context.Context, tx *sql.Tx, teamName string) error {
	var teamExists bool
	if err := tx.QueryRowContext(
		ctx,
		`SELECT EXISTS(SELECT 1 FROM teams WHERE name=$1)`,
		teamName,
	).Scan(&teamExists); err != nil {
		return err
	}
	if !teamExists {
		return ErrTeamNotFound
	}
	return nil
}

func (s *Store) deactivateUsersTx(ctx context.Context, tx *sql.Tx, teamName string) error {
	_, err := tx.ExecContext(ctx, `UPDATE users SET is_active=false WHERE team_name=$1`, teamName)
	return err
}

type assignment struct {
	prID     string
	authorID string
	reviewer string
}

func (s *Store) fetchTeamAssignments(
	ctx context.Context,
	tx *sql.Tx,
	teamName string,
) ([]assignment, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT pr.pr_id, pr.author_id, ar.user_id
FROM pull_requests pr
JOIN assigned_reviewers ar ON ar.pr_id = pr.pr_id
JOIN users u ON u.user_id = ar.user_id
WHERE pr.status=$1 AND u.team_name=$2
FOR UPDATE
`, StatusOpen, teamName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []assignment
	for rows.Next() {
		var a assignment
		if err := rows.Scan(&a.prID, &a.authorID, &a.reviewer); err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return list, nil
}

func (s *Store) reassignAfterDeactivation(
	ctx context.Context,
	tx *sql.Tx,
	teamName string,
	assignments []assignment,
) error {
	for _, a := range assignments {
		currentReviewers, err := s.listReviewersTx(ctx, tx, a.prID)
		if err != nil {
			return err
		}
		block := make(map[string]struct{}, len(currentReviewers)+1)
		for _, r := range currentReviewers {
			block[r] = struct{}{}
		}
		block[a.authorID] = struct{}{}

		candidates, err := s.pickCandidates(ctx, tx, teamName, "", block, 1)
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			if _, err := tx.ExecContext(
				ctx,
				`DELETE FROM assigned_reviewers WHERE pr_id=$1 AND user_id=$2`,
				a.prID,
				a.reviewer,
			); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.ExecContext(
			ctx,
			`DELETE FROM assigned_reviewers WHERE pr_id=$1 AND user_id=$2`,
			a.prID,
			a.reviewer,
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO assigned_reviewers(pr_id, user_id) VALUES ($1,$2)`,
			a.prID,
			candidates[0],
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) fetchPRTx(ctx context.Context, tx *sql.Tx, prID string) (*PullRequest, error) {
	row := tx.QueryRowContext(ctx, `
SELECT pr_id, pr_name, author_id, status, created_at, merged_at
FROM pull_requests
WHERE pr_id=$1
`, prID)
	var pr PullRequest
	if err := row.Scan(&pr.ID, &pr.Name, &pr.AuthorID, &pr.Status, &pr.CreatedAt, &pr.MergedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPRNotFound
		}
		return nil, err
	}
	reviewers, err := s.listReviewersTx(ctx, tx, prID)
	if err != nil {
		return nil, err
	}
	pr.AssignedReviewers = reviewers
	return &pr, nil
}

func (s *Store) listReviewers(ctx context.Context, prID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id FROM assigned_reviewers WHERE pr_id=$1 ORDER BY user_id`, prID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) listReviewersTx(ctx context.Context, tx *sql.Tx, prID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT user_id FROM assigned_reviewers WHERE pr_id=$1 ORDER BY user_id`, prID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) pickCandidates(
	ctx context.Context,
	q querier,
	teamName,
	exclude string,
	block map[string]struct{},
	limit int,
) ([]string, error) {
	rows, err := q.QueryContext(ctx, `
SELECT user_id FROM users
WHERE team_name=$1 AND is_active=true AND user_id<>$2
`, teamName, exclude)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if block != nil {
			if _, ok := block[id]; ok {
				continue
			}
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.rnd.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return ids, nil
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func buildTeam(ctx context.Context, q querier, teamName string) (TeamPayload, error) {
	rows, err := q.QueryContext(ctx, `
SELECT u.user_id, u.username, u.is_active
FROM users u
WHERE u.team_name=$1
ORDER BY u.user_id
`, teamName)
	if err != nil {
		return TeamPayload{}, err
	}
	defer func() { _ = rows.Close() }()
	members := make([]TeamUpserted, 0)
	for rows.Next() {
		var m TeamUpserted
		if err := rows.Scan(&m.UserID, &m.Username, &m.IsActive); err != nil {
			return TeamPayload{}, err
		}
		members = append(members, m)
	}
	if rows.Err() != nil {
		return TeamPayload{}, rows.Err()
	}
	if len(members) == 0 {
		var exists bool
		if err := q.QueryRowContext(
			ctx,
			`SELECT EXISTS(SELECT 1 FROM teams WHERE name=$1)`,
			teamName,
		).Scan(&exists); err != nil {
			return TeamPayload{}, err
		}
		if !exists {
			return TeamPayload{}, ErrTeamNotFound
		}
	}
	return TeamPayload{TeamName: teamName, Members: members}, nil
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate key")
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sql.ErrTxDone) {
		return true
	}
	return isSerializationError(err)
}

func isSerializationError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "SQLSTATE 40001") || strings.Contains(err.Error(), "could not serialize access")
}
