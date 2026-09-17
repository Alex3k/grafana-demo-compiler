package httpapi

import "net/http"

func (s *Server) createPrototype(w http.ResponseWriter, r *http.Request) {
	iteration, fault := s.prototype.Start(r.Context(), r.PathValue("id"))
	if fault != nil {
		s.writeFault(w, fault)
		return
	}
	writeJSON(w, http.StatusAccepted, iteration)
}
