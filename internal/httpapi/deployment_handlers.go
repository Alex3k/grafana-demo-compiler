package httpapi

import "net/http"

func (s *Server) connectStack(w http.ResponseWriter, r *http.Request) {
	item, fault := s.deployment.ConnectStack(r.Context(), r.PathValue("id"))
	if fault != nil {
		s.writeFault(w, fault)
		return
	}
	writeJSON(w, http.StatusAccepted, item)
}

func (s *Server) createStack(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Region string `json:"region"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid stack request", err)
		return
	}
	item, fault := s.deployment.CreateStack(r.Context(), r.PathValue("id"), input.Region)
	if fault != nil {
		s.writeFault(w, fault)
		return
	}
	writeJSON(w, http.StatusAccepted, item)
}

func (s *Server) createDeployment(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Target string `json:"target"`
		Region string `json:"region"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid deployment request", err)
		return
	}
	item, fault := s.deployment.Start(r.Context(), r.PathValue("id"), input.Target, input.Region)
	if fault != nil {
		s.writeFault(w, fault)
		return
	}
	writeJSON(w, http.StatusAccepted, item)
}

func (s *Server) configureDeploymentToken(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(r, &input); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid token request", err)
		return
	}
	item, fault := s.deployment.ConfigureToken(r.Context(), r.PathValue("id"), r.PathValue("deploymentID"), input.Token)
	input.Token = ""
	if fault != nil {
		s.writeFault(w, fault)
		return
	}
	writeJSON(w, http.StatusAccepted, item)
}
