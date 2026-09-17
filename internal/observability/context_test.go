package observability

import (
	"context"
	"testing"

	"github.com/Alex3k/grafana-demo-compiler/internal/contextengine"
)

func TestContextManifestRoundTrip(t *testing.T) {
	manifest := contextengine.Manifest{
		SchemaVersion: contextengine.SchemaVersion,
		Role:          contextengine.RoleCollaborator,
		BriefVersion:  4,
	}
	ctx := WithContextManifest(context.Background(), manifest)
	got, ok := ContextManifestFromContext(ctx)
	if !ok || got.SchemaVersion != manifest.SchemaVersion || got.Role != manifest.Role || got.BriefVersion != manifest.BriefVersion {
		t.Fatalf("ContextManifestFromContext() = %#v, %t", got, ok)
	}
}
