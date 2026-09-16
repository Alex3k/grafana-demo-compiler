package domain

import "time"

type Session struct {
	ID        string       `json:"id"`
	Title     string       `json:"title"`
	State     string       `json:"state"`
	CreatedAt time.Time    `json:"createdAt"`
	UpdatedAt time.Time    `json:"updatedAt"`
	Messages  []Message    `json:"messages,omitempty"`
	Brief     *LivingBrief `json:"brief,omitempty"`
}

type Message struct {
	ID        string    `json:"id"`
	SessionID string    `json:"sessionId"`
	Role      string    `json:"role"`
	Kind      string    `json:"kind"`
	Content   string    `json:"content"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
}

type Operation struct {
	ID        string     `json:"id"`
	SessionID string     `json:"sessionId"`
	Kind      string     `json:"kind"`
	Status    string     `json:"status"`
	Summary   string     `json:"summary"`
	Error     string     `json:"error,omitempty"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
}

type BriefItem struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Status string `json:"status"`
}

type BriefFocus struct {
	Label  string `json:"label"`
	Value  string `json:"value"`
	Status string `json:"status"`
}

type NarrativeBeat struct {
	Stage   string `json:"stage"`
	Detail  string `json:"detail"`
	Minutes int    `json:"minutes"`
}

type BriefDecision struct {
	Summary  string `json:"summary"`
	Status   string `json:"status"`
	Evidence string `json:"evidence"`
}

type BriefScope struct {
	Included           []BriefItem `json:"included"`
	Excluded           []BriefItem `json:"excluded"`
	SimulationBoundary BriefItem   `json:"simulationBoundary"`
}

type PrototypeOffer struct {
	Ready            bool     `json:"ready"`
	Summary          string   `json:"summary"`
	Included         []string `json:"included"`
	Excluded         []string `json:"excluded"`
	RealComponents   []string `json:"realComponents"`
	SimulatedParts   []string `json:"simulatedComponents"`
	Services         []string `json:"services"`
	Scenario         string   `json:"scenario"`
	Telemetry        []string `json:"telemetry"`
	GrafanaResources []string `json:"grafanaResources"`
	Assumptions      []string `json:"assumptions"`
}

type AlignmentEvaluation struct {
	Result           string   `json:"result"`
	Explanation      string   `json:"explanation"`
	Missing          []string `json:"missing"`
	Contradictions   []string `json:"contradictions"`
	UnnecessaryScope []string `json:"unnecessaryScope"`
	Risks            []string `json:"risks"`
	Evidence         []string `json:"evidence"`
}

type PlanAcceptance struct {
	Accepted   bool                `json:"accepted"`
	Evidence   string              `json:"evidence"`
	Evaluation AlignmentEvaluation `json:"evaluation"`
}

type BriefContent struct {
	Changes          []string        `json:"changes"`
	Audience         BriefItem       `json:"audience"`
	Company          BriefItem       `json:"company"`
	Outcome          BriefItem       `json:"outcome"`
	Stakes           BriefItem       `json:"stakes"`
	Scenario         BriefItem       `json:"scenario"`
	Journey          BriefItem       `json:"journey"`
	ProofPoints      []BriefItem     `json:"proofPoints"`
	Scope            BriefScope      `json:"scope"`
	Services         []BriefItem     `json:"services"`
	Telemetry        []BriefItem     `json:"telemetry"`
	GrafanaResources []BriefItem     `json:"grafanaResources"`
	Narrative        []NarrativeBeat `json:"narrative"`
	Mermaid          string          `json:"mermaid"`
	OpenQuestions    []string        `json:"openQuestions"`
	Decisions        []BriefDecision `json:"decisions"`
	PrototypeOffer   PrototypeOffer  `json:"prototypeOffer"`
	Acceptance       PlanAcceptance  `json:"acceptance"`
}

type LivingBrief struct {
	Version   int          `json:"version"`
	UpdatedAt time.Time    `json:"updatedAt"`
	Content   BriefContent `json:"content"`
}
