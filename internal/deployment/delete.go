package deployment

import (
	"context"
	"fmt"
	"path/filepath"
)

// DeleteLocal removes this session's Compose containers, networks and volumes.
func (r *Runner) DeleteLocal(ctx context.Context, root, sessionID string) error {
	if !filepath.IsAbs(root) || shortAlnum(sessionID, 10) == "" {
		return fmt.Errorf("invalid local deletion target")
	}
	_, err := runIn(ctx, root, "docker", "compose", "--project-name", "democompiler"+shortAlnum(sessionID, 10), "down", "--remove-orphans", "--volumes")
	return err
}

func (r *Runner) DeleteStack(ctx context.Context, slug string) error {
	if !validSlug(slug) {
		return fmt.Errorf("invalid stack slug")
	}
	if _, err := run(ctx, "gcx", "cloud", "stacks", "get", slug, "-o", "json"); err != nil {
		if isNotFound(err) {
			return nil
		}
		return err
	}
	if _, err := run(ctx, "gcx", "cloud", "stacks", "update", slug, "--no-delete-protection", "-o", "json"); err != nil {
		return err
	}
	if _, err := run(ctx, "gcx", "cloud", "stacks", "delete", slug, "--force", "-o", "json"); err != nil {
		return err
	}
	if _, err := run(ctx, "gcx", "cloud", "stacks", "get", slug, "-o", "json"); err == nil {
		return fmt.Errorf("stack deletion is still pending; retry deletion shortly")
	} else if !isNotFound(err) {
		return err
	}
	return nil
}
