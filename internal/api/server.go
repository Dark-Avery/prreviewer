package api

import (
	"net/http"

	"prreviewer/internal/service"

	"go.uber.org/zap"
)

type server struct {
	svc    *service.Service
	logger *zap.SugaredLogger
}

func NewServer(svc *service.Service, logger *zap.SugaredLogger) *server {
	return &server{svc: svc, logger: logger}
}

func (s *server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)

	// teams
	mux.HandleFunc("POST /team/add", s.handleAddTeam)
	mux.HandleFunc("POST /team/deactivate", s.handleDeactivateTeam)
	mux.HandleFunc("GET /team/get", s.handleGetTeam)

	// users
	mux.HandleFunc("POST /users/setIsActive", s.handleSetIsActive)
	mux.HandleFunc("GET /users/getReview", s.handleGetReview)

	// pull requests
	mux.HandleFunc("POST /pullRequest/create", s.handleCreatePR)
	mux.HandleFunc("POST /pullRequest/merge", s.handleMergePR)
	mux.HandleFunc("POST /pullRequest/reassign", s.handleReassign)

	// stats
	mux.HandleFunc("GET /stats", s.handleStats)
	return mux
}
