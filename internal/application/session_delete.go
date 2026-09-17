package application

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Alex3k/grafana-demo-compiler/internal/store"
)

type sessionCleaner interface {
	DeleteLocal(context.Context, string, string) error
	DeleteStack(context.Context, string) error
}

// DeleteSession leaves the durable deletion marker and session intact on error,
// allowing retry without starting new work against partially deleted resources.
func (s *DeploymentService) DeleteSession(ctx context.Context, data *store.Store, id string) error {
	s.localMu.Lock()
	defer s.localMu.Unlock()
	session, err := data.GetSession(ctx, id)
	if err != nil {
		return err
	}
	cleaner, ok := s.runner.(sessionCleaner)
	if !ok {
		return fmt.Errorf("session cleanup is unavailable")
	}
	if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
		return fmt.Errorf("invalid session deletion target")
	}
	root, err := filepath.Abs(s.root)
	if err != nil {
		return err
	}
	sessionRoot := filepath.Join(root, id)
	// Resolve existing ancestors before destructive work; never follow a session symlink.
	if info, err := os.Lstat(sessionRoot); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("session directory is not a regular directory")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := data.BeginSessionDeletion(ctx, id); err != nil {
		return err
	}
	// Reload after the atomic claim: a stack may have finished provisioning
	// since the initial lookup, but new stack claims are now blocked.
	session, err = data.GetSession(ctx, id)
	if err != nil {
		return err
	}
	expectedSlug := "democompiler" + id[:12]
	if session.GrafanaStack != nil && session.GrafanaStack.StackSlug != expectedSlug {
		return fmt.Errorf("recorded stack does not match this session")
	}
	roots := map[string]bool{}
	for _, d := range session.Deployments {
		if d.StackSlug != expectedSlug {
			return fmt.Errorf("recorded stack does not match this session")
		}
		for _, p := range session.Prototypes {
			if p.ID == d.PrototypeIterationID {
				real, err := filepath.EvalSymlinks(p.RootPath)
				if err != nil {
					return fmt.Errorf("cannot inspect deployed files: %w", err)
				}
				realRoot, err := filepath.EvalSymlinks(sessionRoot)
				if err != nil || real == realRoot || !pathWithinRoot(realRoot, real) {
					return fmt.Errorf("prototype path is outside this session")
				}
				roots[real] = true
			}
		}
	}
	for path := range roots {
		if err := cleaner.DeleteLocal(ctx, path, id); err != nil {
			return fmt.Errorf("delete local demo: %w", err)
		}
	}
	if session.GrafanaStack != nil || len(session.Deployments) > 0 {
		if err := cleaner.DeleteStack(ctx, expectedSlug); err != nil {
			return fmt.Errorf("delete Grafana Cloud stack: %w", err)
		}
	}
	if err := os.RemoveAll(sessionRoot); err != nil {
		return fmt.Errorf("remove generated files: %w", err)
	}
	return data.DeleteSession(ctx, id)
}
