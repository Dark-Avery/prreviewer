package service

import (
	"context"

	"prreviewer/internal/storage"
)

// Service orchestrates application logic between HTTP layer and storage.
type Service struct {
	store Store
}

// Store defines minimal storage contract used by the service.
type Store interface {
	AddTeam(ctx context.Context, payload storage.TeamPayload) (storage.TeamPayload, error)
	GetTeam(ctx context.Context, teamName string) (storage.TeamPayload, error)
	SetUserActive(ctx context.Context, payload storage.SetActivePayload) (*storage.User, error)
	CreatePR(ctx context.Context, payload storage.CreatePRPayload) (*storage.PullRequest, error)
	MergePR(ctx context.Context, id string) (*storage.PullRequest, error)
	Reassign(ctx context.Context, payload storage.ReassignPayload) (*storage.PullRequest, string, error)
	UserReviews(ctx context.Context, userID string) ([]storage.PullRequestShort, error)
	Stats(ctx context.Context) (*storage.Stats, error)
	MassDeactivate(ctx context.Context, teamName string) error
}

func New(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) AddTeam(ctx context.Context, payload storage.TeamPayload) (storage.TeamPayload, error) {
	return s.store.AddTeam(ctx, payload)
}

func (s *Service) DeactivateTeam(ctx context.Context, teamName string) error {
	return s.store.MassDeactivate(ctx, teamName)
}

func (s *Service) GetTeam(ctx context.Context, teamName string) (storage.TeamPayload, error) {
	return s.store.GetTeam(ctx, teamName)
}

func (s *Service) SetUserActive(ctx context.Context, payload storage.SetActivePayload) (*storage.User, error) {
	return s.store.SetUserActive(ctx, payload)
}

func (s *Service) CreatePR(ctx context.Context, payload storage.CreatePRPayload) (*storage.PullRequest, error) {
	return s.store.CreatePR(ctx, payload)
}

func (s *Service) MergePR(ctx context.Context, id string) (*storage.PullRequest, error) {
	return s.store.MergePR(ctx, id)
}

func (s *Service) Reassign(ctx context.Context, payload storage.ReassignPayload) (*storage.PullRequest, string, error) {
	return s.store.Reassign(ctx, payload)
}

func (s *Service) UserReviews(ctx context.Context, userID string) ([]storage.PullRequestShort, error) {
	return s.store.UserReviews(ctx, userID)
}

func (s *Service) Stats(ctx context.Context) (*storage.Stats, error) {
	return s.store.Stats(ctx)
}
