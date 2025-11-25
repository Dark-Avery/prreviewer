package api

import (
	"net/http"

	"prreviewer/internal/storage"
)

func (s *server) handleAddTeam(w http.ResponseWriter, r *http.Request) {
	var payload storage.TeamPayload
	if err := decodeJSON(r, &payload); err != nil {
		s.logger.Warnw("invalid json", "err", err)
		writeJSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), s.logger)
		return
	}
	if payload.TeamName == "" {
		writeJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "team_name is required", s.logger)
		return
	}
	team, err := s.svc.AddTeam(r.Context(), payload)
	if err != nil {
		writeJSONAPIError(w, mapErrorWithLog(s.logger, err), s.logger)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"team": team}, s.logger)
}

func (s *server) handleDeactivateTeam(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		TeamName string `json:"team_name"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		s.logger.Warnw("invalid json", "err", err)
		writeJSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), s.logger)
		return
	}
	if payload.TeamName == "" {
		writeJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "team_name is required", s.logger)
		return
	}
	if err := s.svc.DeactivateTeam(r.Context(), payload.TeamName); err != nil {
		writeJSONAPIError(w, mapErrorWithLog(s.logger, err), s.logger)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"team_name": payload.TeamName, "status": "deactivated"}, s.logger)
}

func (s *server) handleGetTeam(w http.ResponseWriter, r *http.Request) {
	teamName := r.URL.Query().Get("team_name")
	if teamName == "" {
		writeJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "team_name is required", s.logger)
		return
	}
	team, err := s.svc.GetTeam(r.Context(), teamName)
	if err != nil {
		writeJSONAPIError(w, mapErrorWithLog(s.logger, err), s.logger)
		return
	}
	writeJSON(w, http.StatusOK, team, s.logger)
}
