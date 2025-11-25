package api

import (
	"net/http"

	"prreviewer/internal/storage"
)

func (s *server) handleCreatePR(w http.ResponseWriter, r *http.Request) {
	var payload storage.CreatePRPayload
	if err := decodeJSON(r, &payload); err != nil {
		s.logger.Warnw("invalid json", "err", err)
		writeJSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), s.logger)
		return
	}
	if payload.ID == "" || payload.Author == "" || payload.Name == "" {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"BAD_REQUEST",
			"pull_request_id, pull_request_name and author_id are required",
			s.logger,
		)
		return
	}
	pr, err := s.svc.CreatePR(r.Context(), payload)
	if err != nil {
		writeJSONAPIError(w, mapErrorWithLog(s.logger, err), s.logger)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"pr": pr}, s.logger)
}

func (s *server) handleMergePR(w http.ResponseWriter, r *http.Request) {
	var payload storage.MergePayload
	if err := decodeJSON(r, &payload); err != nil {
		s.logger.Warnw("invalid json", "err", err)
		writeJSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), s.logger)
		return
	}
	if payload.ID == "" {
		writeJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "pull_request_id is required", s.logger)
		return
	}
	pr, err := s.svc.MergePR(r.Context(), payload.ID)
	if err != nil {
		writeJSONAPIError(w, mapErrorWithLog(s.logger, err), s.logger)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pr": pr}, s.logger)
}

func (s *server) handleReassign(w http.ResponseWriter, r *http.Request) {
	var payload storage.ReassignPayload
	if err := decodeJSON(r, &payload); err != nil {
		s.logger.Warnw("invalid json", "err", err)
		writeJSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), s.logger)
		return
	}
	if payload.Old == "" || payload.PRID == "" {
		writeJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "pull_request_id and old_user_id are required", s.logger)
		return
	}
	pr, replacedBy, err := s.svc.Reassign(r.Context(), payload)
	if err != nil {
		writeJSONAPIError(w, mapErrorWithLog(s.logger, err), s.logger)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pr": pr, "replaced_by": replacedBy}, s.logger)
}
