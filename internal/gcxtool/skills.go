package gcxtool

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const maxSkillBytes = 128 * 1024

var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var skillReferencePattern = regexp.MustCompile(`^references/[a-zA-Z0-9_./-]+$`)

// ReadSkill reads only the installed CLI's embedded skill bundle. It needs no
// stack or authentication and never installs skills or reads arbitrary files.
// An empty name returns the skill catalog; reference selects one bundled file.
func ReadSkill(ctx context.Context, name, reference string) (string, error) {
	args := []string{"agent", "skills", "list", "-o", "json"}
	if name == "" {
		if reference != "" {
			return "", errors.New("a skill name is required for a reference")
		}
	} else {
		if len(name) > 100 || !skillNamePattern.MatchString(name) {
			return "", errors.New("invalid bundled skill name; use the catalog name")
		}
		args = []string{"agent", "skills", "get", name}
		if reference != "" {
			if len(reference) > 512 || !skillReferencePattern.MatchString(reference) {
				return "", errors.New("skill reference must be a relative references/... path")
			}
			for _, part := range strings.Split(reference, "/") {
				if part == "" || part == "." || part == ".." {
					return "", errors.New("skill reference cannot contain empty or traversal segments")
				}
			}
			args = append(args, reference)
		}
		args = append(args, "-o", "text")
	}
	// Ask for one byte extra so an oversized instruction is rejected, never
	// silently delivered as a partial skill. run also appends a truncation marker.
	output, err := run(ctx, "", args, "", maxSkillBytes+1)
	if err != nil {
		return "", fmt.Errorf("could not read bundled gcx skill: %w: %s", err, Redact(output))
	}
	if len(output) > maxSkillBytes {
		return "", errors.New("bundled gcx skill exceeds 128 KiB; no partial instructions were returned")
	}
	return output, nil
}
