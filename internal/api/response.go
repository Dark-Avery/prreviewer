package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"prreviewer/internal/storage"

	"go.uber.org/zap"
)

type apiError struct {
	HTTPStatus int
	Code       string
	Message    string
}

func decodeJSON(r *http.Request, v any) error {
	defer func() { _ = r.Body.Close() }()
	decoder := json.NewDecoder(r.Body)
	return decoder.Decode(v)
}

func writeJSON(w http.ResponseWriter, status int, payload any, logger *zap.SugaredLogger) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		if logger != nil {
			logger.Errorw("failed to encode response", "err", err)
		}
	}
}

func writeJSONError(w http.ResponseWriter, status int, code, message string, logger *zap.SugaredLogger) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	}, logger)
}

func writeJSONAPIError(w http.ResponseWriter, apiErr *apiError, logger *zap.SugaredLogger) {
	if apiErr == nil {
		writeJSONError(w, http.StatusInternalServerError, "INTERNAL", "internal error", logger)
		return
	}
	writeJSONError(w, apiErr.HTTPStatus, apiErr.Code, apiErr.Message, logger)
}

func mapErrorWithLog(logger *zap.SugaredLogger, err error) *apiError {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return &apiError{HTTPStatus: 499, Code: "CLIENT_CLOSED", Message: "client canceled request"}
	case errors.Is(err, storage.ErrTeamExists):
		return &apiError{HTTPStatus: http.StatusBadRequest, Code: "TEAM_EXISTS", Message: "team_name already exists"}
	case errors.Is(err, storage.ErrPRExists):
		return &apiError{HTTPStatus: http.StatusConflict, Code: "PR_EXISTS", Message: "PR id already exists"}
	case errors.Is(err, storage.ErrPRMerged):
		return &apiError{HTTPStatus: http.StatusConflict, Code: "PR_MERGED", Message: "cannot reassign on merged PR"}
	case errors.Is(err, storage.ErrNotAssigned):
		return &apiError{
			HTTPStatus: http.StatusConflict,
			Code:       "NOT_ASSIGNED",
			Message:    "reviewer is not assigned to this PR",
		}
	case errors.Is(err, storage.ErrNoCandidate):
		return &apiError{
			HTTPStatus: http.StatusConflict,
			Code:       "NO_CANDIDATE",
			Message:    "no active replacement candidate in team",
		}
	case errors.Is(err, storage.ErrUserNotFound),
		errors.Is(err, storage.ErrPRNotFound),
		errors.Is(err, storage.ErrTeamNotFound):
		return &apiError{HTTPStatus: http.StatusNotFound, Code: "NOT_FOUND", Message: "resource not found"}
	default:
		if logger != nil {
			logger.Errorw("unexpected error", "err", err)
		}
		return &apiError{HTTPStatus: http.StatusInternalServerError, Code: "INTERNAL", Message: "internal error"}
	}
}
