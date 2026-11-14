package domain

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// MaxSparsePaths bounds the directories in one sparse checkout cone, for both
// a spawn's requested paths and a project's alwaysInclude list.
const MaxSparsePaths = 50

// SparseCheckoutConfig controls cone-mode sparse worktrees for plan milestone
// workers and `ao spawn --sparse`.
type SparseCheckoutConfig struct {
	// Enabled lets plan milestones and `ao spawn --sparse` create sparse
	// worktrees. Default false.
	Enabled bool `json:"enabled,omitempty"`
	// AlwaysInclude are repo-relative directories added to every sparse
	// checkout (shared libraries, tooling).
	AlwaysInclude []string `json:"alwaysInclude,omitempty"`
}

// Validate checks the alwaysInclude list.
func (c SparseCheckoutConfig) Validate() error {
	if len(c.AlwaysInclude) > MaxSparsePaths {
		return fmt.Errorf("sparseCheckout.alwaysInclude: at most %d paths", MaxSparsePaths)
	}
	if err := ValidateSparsePaths(c.AlwaysInclude); err != nil {
		return fmt.Errorf("sparseCheckout.alwaysInclude: %w", err)
	}
	return nil
}

// ValidateSparsePaths checks that every entry names a repo-relative directory
// inside the repository (not the root itself).
func ValidateSparsePaths(paths []string) error {
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			return fmt.Errorf("sparse paths must not be empty")
		}
		if err := validateRepoRelative(p); err != nil {
			return fmt.Errorf("%q: %w", p, err)
		}
	}
	return nil
}

// EffectiveSparsePaths is the cone a sparse spawn checks out: the requested
// directories plus the project's alwaysInclude, cleaned, de-duplicated, and
// sorted. No requested directories means a full checkout (nil), whatever the
// project includes.
func EffectiveSparsePaths(requested, alwaysInclude []string) []string {
	if len(requested) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, list := range [][]string{requested, alwaysInclude} {
		for _, p := range list {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			p = path.Clean(strings.ReplaceAll(p, `\`, "/"))
			if seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
