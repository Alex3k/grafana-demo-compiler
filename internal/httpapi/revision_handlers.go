package httpapi

import (
	"context"
	"net/http"
	"time"
)

func (s *Server) listRevisions(w http.ResponseWriter, r *http.Request) {
	if _, err := s.store.GetSession(r.Context(), r.PathValue("id")); err != nil {
		s.writeError(w, 404, "Session not found", err)
		return
	}
	items, err := s.store.ListRevisions(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeError(w, 500, "Could not load revisions", err)
		return
	}
	writeJSON(w, 200, items)
}
func (s *Server) decideRevision(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Decision string `json:"decision"`
	}
	if err := decodeJSON(r, &input); err != nil || (input.Decision != "approve" && input.Decision != "reject") {
		writeJSON(w, 400, map[string]string{"error": "Choose approve or reject"})
		return
	}
	if s.prototype == nil {
		writeJSON(w, 503, map[string]string{"error": "Builder unavailable"})
		return
	}
	status := "approved"
	if input.Decision == "reject" {
		status = "rejected"
	}
	proposal, err := s.store.ClaimRevision(r.Context(), r.PathValue("id"), r.PathValue("revisionID"), status)
	if err != nil {
		s.writeError(w, 409, "This proposal is no longer pending", err)
		return
	}
	if status == "rejected" {
		writeJSON(w, 200, proposal)
		return
	}
	iteration, fault := s.prototype.StartRevision(r.Context(), proposal.SessionID, proposal)
	persist, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if fault != nil {
		_ = s.store.FinishRevisionApproval(persist, proposal.ID, "", fault.Public)
		s.writeFault(w, fault)
		return
	}
	if err := s.store.FinishRevisionApproval(persist, proposal.ID, iteration.ID, ""); err != nil {
		s.writeError(w, 500, "Build started, but its approval receipt could not be updated. Do not retry; inspect the build status.", err)
		return
	}
	writeJSON(w, 202, iteration)
}
