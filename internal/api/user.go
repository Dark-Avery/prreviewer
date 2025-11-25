package api

import (
	"net/http"

	"prreviewer/internal/storage"
)

func (s *server) handleSetIsActive(w http.ResponseWriter, r *http.Request) {
	var payload storage.SetActivePayload
	if err := decodeJSON(r, &payload); err != nil {
		s.logger.Warnw("invalid json", "err", err)
		writeJSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), s.logger)
		return
	}
	if payload.UserID == "" {
		writeJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "user_id is required", s.logger)
		return
	}
	user, err := s.svc.SetUserActive(r.Context(), payload)
	if err != nil {
		writeJSONAPIError(w, mapErrorWithLog(s.logger, err), s.logger)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user}, s.logger)
}

func (s *server) handleGetReview(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		writeJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "user_id is required", s.logger)
		return
	}
	prs, err := s.svc.UserReviews(r.Context(), userID)
	if err != nil {
		writeJSONAPIError(w, mapErrorWithLog(s.logger, err), s.logger)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":       userID,
		"pull_requests": prs,
	}, s.logger)
}
