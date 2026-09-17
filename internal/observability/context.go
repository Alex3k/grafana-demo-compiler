package observability

import (
	"context"

	"github.com/Alex3k/grafana-demo-compiler/internal/contextengine"
)

type contextManifestKey struct{}

// WithContextManifest attaches a content-free description of the context
// selected for one model generation. The manifest contains counts and topic
// identifiers only, so it is safe to expose as observability metadata.
func WithContextManifest(ctx context.Context, manifest contextengine.Manifest) context.Context {
	return context.WithValue(ctx, contextManifestKey{}, manifest)
}

func ContextManifestFromContext(ctx context.Context) (contextengine.Manifest, bool) {
	manifest, ok := ctx.Value(contextManifestKey{}).(contextengine.Manifest)
	return manifest, ok
}
