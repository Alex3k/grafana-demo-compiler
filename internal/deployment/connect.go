package deployment

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Alex3k/grafana-demo-compiler/internal/gcxtool"
)

// ConnectStack deliberately uses a per-stack credential file. OAuth must not
// replace the user's current gcx context (which supplies Cloud provisioning).
func (r *Runner) ConnectStack(ctx context.Context, slug, server string, progress func(string)) error {
	if !validSlug(slug) || strings.TrimRight(server, "/") != "https://"+slug+".grafana.net" {
		return errors.New("invalid Grafana stack connection target")
	}
	config, err := gcxtool.StackConfigPath(slug)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
		return err
	}
	verify := func() error {
		if _, err := os.Stat(config); err != nil {
			return err
		}
		if err := gcxtool.CheckContext(ctx, slug); err != nil {
			return err
		}
		checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if err := runStackAuth(checkCtx, "config", "check", "--config", config, "--context", slug); err != nil {
			return err
		}
		return runStackAuth(checkCtx, "datasources", "list", "--config", config, "--context", slug, "-o", "json")
	}
	progress("Checking the saved Grafana connection")
	if verify() == nil {
		return nil
	}
	progress("Awaiting browser approval: complete Grafana sign-in in the browser opened on this computer")
	if err := runStackAuth(ctx, "login", slug, "--server", "https://"+slug+".grafana.net", "--oauth", "--yes", "--config", config); err != nil {
		return errors.New("Grafana browser approval did not complete")
	}
	if err := os.Chmod(config, 0600); err != nil {
		return err
	}
	progress("Verifying Grafana access")
	return verify()
}

func runStackAuth(ctx context.Context, args ...string) error {
	path, err := executable("gcx")
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	for _, key := range []string{"HOME", "PATH", "USER", "TMPDIR", "XDG_CONFIG_HOME", "SSL_CERT_FILE", "DISPLAY", "WAYLAND_DISPLAY", "DBUS_SESSION_BUS_ADDRESS"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	// OAuth is a user-facing browser flow. gcx intentionally suppresses browser
	// launches in agent mode; the allowlist above also drops ambient agent flags.
	if len(args) == 0 || args[0] != "login" {
		cmd.Env = append(cmd.Env, "GCX_AGENT_MODE=1")
	}
	// OAuth URLs and credential-bearing command output must never enter session
	// history, logs, or model context. gcx opens the browser itself.
	return cmd.Run()
}
