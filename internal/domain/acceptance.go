package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// AcceptanceHash identifies the brief being approved, excluding its change log
// and approval/evaluation metadata so recording approval does not change identity.
func (content BriefContent) AcceptanceHash() string {
	content.Changes = nil
	content.Acceptance = PlanAcceptance{}
	payload, _ := json.Marshal(content)
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
