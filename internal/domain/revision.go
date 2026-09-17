package domain

type RevisionProposal struct {
	ID              string   `json:"id"`
	SessionID       string   `json:"sessionId"`
	BaseIterationID string   `json:"baseIterationId"`
	BriefVersion    int      `json:"briefVersion"`
	SourceDigest    string   `json:"sourceDigest"`
	Goal            string   `json:"goal"`
	Evidence        string   `json:"evidence"`
	Files           []string `json:"files"`
	Status          string   `json:"status"`
	IterationID     string   `json:"iterationId"`
	Error           string   `json:"error"`
}
