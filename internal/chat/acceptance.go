package chat

import (
	"errors"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

// bindAcceptance preserves an existing approval only for the same brief. A
// changed brief always needs a subsequent explicit acceptance, even if the
// curator carries forward accepted=true.
func bindAcceptance(previous *domain.BriefContent, candidate *domain.BriefContent, messages []domain.Message) error {
	hash := candidate.AcceptanceHash()
	if previous != nil && previous.AcceptanceHash() != hash {
		candidate.Acceptance = domain.PlanAcceptance{}
		return nil
	}
	if !candidate.Acceptance.Accepted {
		candidate.Acceptance = domain.PlanAcceptance{}
		return nil
	}
	if previous != nil && previous.Acceptance.Accepted {
		if previous.Acceptance.BriefHash != hash || previous.Acceptance.ProposalMessageID == "" || previous.Acceptance.AcceptingMessageID == "" {
			// Legacy approvals have no reliable identity. Do not guess one.
			candidate.Acceptance = domain.PlanAcceptance{}
			return nil
		}
		candidate.Acceptance = previous.Acceptance
		return nil
	}
	candidate.Acceptance.BriefHash = hash
	_, err := acceptedPlan(*candidate, messages)
	return err
}

// acceptedPlan resolves explicit references; conversation recency is never
// used to choose which proposal is evaluated.
func acceptedPlan(brief domain.BriefContent, messages []domain.Message) (string, error) {
	acceptance := brief.Acceptance
	if !acceptance.Accepted || acceptance.BriefHash != brief.AcceptanceHash() || acceptance.ProposalMessageID == "" || acceptance.AcceptingMessageID == "" {
		return "", errors.New("plan acceptance is missing or stale; explicitly accept the current plan again")
	}
	proposalIndex, acceptingIndex := -1, -1
	var plan string
	for index, message := range messages {
		if message.Kind != "message" || message.Status != "complete" || message.Content == "" {
			continue
		}
		if message.ID == acceptance.ProposalMessageID && message.Role == "assistant" {
			proposalIndex, plan = index, message.Content
		}
		if message.ID == acceptance.AcceptingMessageID && message.Role == "user" {
			acceptingIndex = index
		}
	}
	if proposalIndex < 0 || acceptingIndex <= proposalIndex {
		return "", errors.New("acceptance must reference a completed assistant proposal and a subsequent user message")
	}
	return plan, nil
}
