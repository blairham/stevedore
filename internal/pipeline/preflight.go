// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/blairham/stevedore/internal/run"
)

// allPinned reports whether every planned image's version is pinned, so the
// run reads no registry to derive a version (a split leg or matrix job given
// the plan's pins).
func allPinned(plans []ImagePlan, pins map[string]string) bool {
	if len(plans) == 0 {
		return false
	}
	for _, plan := range plans {
		if _, ok := pins[plan.Image.ID]; !ok {
			return false
		}
	}
	return true
}

// checkSecrets refuses a real release that would build without a declared
// env-backed secret. The builder leaves an unset one out (so a snapshot can
// build without it), and the build then fails deep inside — a private module
// fetch with no credentials — instead of here, naming the variable.
func checkSecrets(plans []ImagePlan) error {
	var errs []error
	for _, plan := range plans {
		for _, s := range plan.Image.Secrets {
			env := s.EnvName()
			if env == "" || s.Optional {
				continue
			}
			if os.Getenv(env) == "" {
				errs = append(errs, fmt.Errorf("image %s: secret %q: $%s is unset; set it, mark the secret `optional: true`, or use --snapshot", plan.Image.ID, s.ID, env))
			}
		}
	}
	return errors.Join(errs...)
}

// ghTokenEnv are the variables gh authenticates from without a stored login.
var ghTokenEnv = []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"}

// checkGitHubAuth verifies gh can authenticate before anything is pushed.
// Finding gh on PATH is not enough: an unauthenticated gh fails at `gh release
// create`, after the images are out, and the release has to be cut by hand.
func checkGitHubAuth(o Options) error {
	for _, env := range ghTokenEnv {
		if os.Getenv(env) != "" {
			return nil
		}
	}
	if _, err := run.Query(o.context(), "gh", "auth", "status"); err != nil {
		detail := err.Error()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if msg := strings.TrimSpace(string(ee.Stderr)); msg != "" {
				detail = msg
			}
		}
		return fmt.Errorf("release.github is enabled but gh is not authenticated (%s); set GH_TOKEN, run `gh auth login`, or pass --skip-publish", detail)
	}
	return nil
}
