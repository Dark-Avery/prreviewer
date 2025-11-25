package service

import (
	"context"
	"errors"
	"testing"

	"prreviewer/internal/storage"
)

type fakeStore struct {
	err error
}

func (f *fakeStore) AddTeam(context.Context, storage.TeamPayload) (storage.TeamPayload, error) {
	return storage.TeamPayload{}, f.err
}

func (f *fakeStore) GetTeam(context.Context, string) (storage.TeamPayload, error) {
	return storage.TeamPayload{}, f.err
}

func (f *fakeStore) SetUserActive(context.Context, storage.SetActivePayload) (*storage.User, error) {
	return nil, f.err
}

func (f *fakeStore) CreatePR(context.Context, storage.CreatePRPayload) (*storage.PullRequest, error) {
	return nil, f.err
}

func (f *fakeStore) MergePR(context.Context, string) (*storage.PullRequest, error) {
	return nil, f.err
}

func (f *fakeStore) Reassign(context.Context, storage.ReassignPayload) (*storage.PullRequest, string, error) {
	return nil, "", f.err
}

func (f *fakeStore) UserReviews(context.Context, string) ([]storage.PullRequestShort, error) {
	return nil, f.err
}

func (f *fakeStore) Stats(context.Context) (*storage.Stats, error) {
	return nil, f.err
}

func (f *fakeStore) MassDeactivate(context.Context, string) error {
	return f.err
}

func TestServicePropagatesError(t *testing.T) {
	wantErr := errors.New("boom")
	s := New(&fakeStore{err: wantErr})

	ctx := context.Background()
	if _, err := s.AddTeam(ctx, storage.TeamPayload{}); !errors.Is(err, wantErr) {
		t.Fatalf("AddTeam err = %v, want %v", err, wantErr)
	}
	if err := s.DeactivateTeam(ctx, "team"); !errors.Is(err, wantErr) {
		t.Fatalf("DeactivateTeam err = %v, want %v", err, wantErr)
	}
	if _, err := s.GetTeam(ctx, "team"); !errors.Is(err, wantErr) {
		t.Fatalf("GetTeam err = %v, want %v", err, wantErr)
	}
	if _, err := s.SetUserActive(ctx, storage.SetActivePayload{}); !errors.Is(err, wantErr) {
		t.Fatalf("SetUserActive err = %v, want %v", err, wantErr)
	}
	if _, err := s.CreatePR(ctx, storage.CreatePRPayload{}); !errors.Is(err, wantErr) {
		t.Fatalf("CreatePR err = %v, want %v", err, wantErr)
	}
	if _, err := s.MergePR(ctx, "pr"); !errors.Is(err, wantErr) {
		t.Fatalf("MergePR err = %v, want %v", err, wantErr)
	}
	if _, _, err := s.Reassign(ctx, storage.ReassignPayload{}); !errors.Is(err, wantErr) {
		t.Fatalf("Reassign err = %v, want %v", err, wantErr)
	}
	if _, err := s.UserReviews(ctx, "u"); !errors.Is(err, wantErr) {
		t.Fatalf("UserReviews err = %v, want %v", err, wantErr)
	}
	if _, err := s.Stats(ctx); !errors.Is(err, wantErr) {
		t.Fatalf("Stats err = %v, want %v", err, wantErr)
	}
}
