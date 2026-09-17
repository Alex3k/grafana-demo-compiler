package chat

import (
	"context"
	"encoding/json"
	"errors"
	aisdk "github.com/grafana/ai-sdk"
	"sort"
	"time"

	"github.com/Alex3k/grafana-demo-compiler/internal/contextengine"
	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

// Later tool steps contain the accumulated provider messages, including system
// context. Count those once and retain the same provider and safety reserves.
func (s *Service) reportStepUsage(sessionID string, initial contextengine.Manifest) aisdk.PrepareStepFunc {
	return func(state aisdk.PrepareStepState) (*aisdk.PrepareStepResult, error) {
		if state.StepNumber == 0 {
			return nil, nil
		}
		payload, err := json.Marshal(state.Messages)
		if err != nil {
			return nil, err
		}
		current := initial
		config := s.configForPrompt("", 0)
		current.EstimatedTokens = estimateTokens(payload, config.BytesPerToken) + initial.ProviderOverheadTokens + initial.SafetyMarginTokens + 256
		s.recordContextUsage(sessionID, current)
		return nil, nil
	}
}

// ContextUsage is a content-free snapshot of the latest request for one role.
// It measures the configured input budget, including reserved overhead.
type ContextUsage struct {
	Role            string    `json:"role"`
	EstimatedTokens int       `json:"estimatedTokens"`
	MaxInputTokens  int       `json:"maxInputTokens"`
	Truncated       bool      `json:"truncated"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

func (s *Service) recordContextUsage(sessionID string, manifest contextengine.Manifest) {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	if s.usage == nil {
		s.usage = make(map[string]map[contextengine.Role]ContextUsage)
	}
	if s.usage[sessionID] == nil {
		s.usage[sessionID] = make(map[contextengine.Role]ContextUsage)
	}
	s.usage[sessionID][manifest.Role] = ContextUsage{
		Role: string(manifest.Role), EstimatedTokens: manifest.EstimatedTokens,
		MaxInputTokens: manifest.MaxInputTokens, Truncated: manifest.Truncated, UpdatedAt: time.Now().UTC(),
	}
}

func (s *Service) ContextUsage(sessionID string) []ContextUsage {
	result := make([]ContextUsage, 0)
	if s == nil {
		return result
	}
	s.usageMu.RLock()
	defer s.usageMu.RUnlock()
	for _, usage := range s.usage[sessionID] {
		result = append(result, usage)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Role < result[j].Role })
	return result
}

func (s *Service) recordContextOverflow(sessionID string, err error) {
	var overflow *contextengine.BudgetOverflowError
	if errors.As(err, &overflow) {
		s.recordContextUsage(sessionID, contextengine.Manifest{
			Role: overflow.Role, EstimatedTokens: overflow.RequiredTokens, MaxInputTokens: overflow.MaxInputTokens,
		})
	}
}

func (s *Service) roleContext(ctx context.Context, session domain.Session, role string, manifest contextengine.Manifest, parents ...string) (context.Context, string) {
	s.recordContextUsage(session.ID, manifest)
	return roleContext(ctx, session, role, manifest, parents...)
}
