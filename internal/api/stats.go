package api

import "net/http"

func (s *server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.svc.Stats(r.Context())
	if err != nil {
		writeJSONAPIError(w, mapErrorWithLog(s.logger, err), s.logger)
		return
	}
	writeJSON(w, http.StatusOK, stats, s.logger)
}
