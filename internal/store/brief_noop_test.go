package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

func TestBriefNoOpAdvancesCursorWithoutRevisionOrContentChange(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "compiler.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	session, err := s.CreateSession(ctx, "Audience recap")
	if err != nil {
		t.Fatal(err)
	}
	brief, err := s.SaveBriefWithCursor(ctx, session.ID, "first", domain.BriefContent{Audience: domain.BriefItem{Value: "Arlo platform team", Status: "confirmed"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceBriefCursor(ctx, session.ID, brief.Version, "recap"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetBrief(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != brief.Version || got.SourceMessageID != "recap" || !reflect.DeepEqual(got.Content, brief.Content) || !got.UpdatedAt.Equal(brief.UpdatedAt) {
		t.Fatalf("no-op changed brief or failed to advance cursor: %#v", got)
	}
	newer, err := s.SaveBriefWithCursor(ctx, session.ID, "new-decision", domain.BriefContent{Audience: domain.BriefItem{Value: "Updated audience", Status: "confirmed"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceBriefCursor(ctx, session.ID, brief.Version, "stale-recap"); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetBrief(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, newer) {
		t.Fatalf("stale no-op modified newer brief: %#v", got)
	}
}
