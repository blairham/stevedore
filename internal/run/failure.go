// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package run

import (
	"fmt"
	"strings"
)

// Limits on how much of a failed command's output an error carries: the tail,
// where tools put the reason, without pasting a whole log into one line.
const (
	maxOutputLines = 10
	maxOutputBytes = 1000
)

// failure is the error for a command that failed: the exit status, the
// command's own (trimmed) error output, and — when that output names a
// failure common enough to recognize — what to do about it. Without the
// output, a missing repository read as "aws: exit status 254".
func failure(name string, err error, output []byte) error {
	msg := trimOutput(string(output))
	if msg == "" {
		return fmt.Errorf("%s: %w", name, err)
	}
	if hint := hintFor(msg); hint != "" {
		return fmt.Errorf("%s: %w: %s (%s)", name, err, msg, hint)
	}
	return fmt.Errorf("%s: %w: %s", name, err, msg)
}

// trimOutput keeps the last few non-blank lines of output, joined into one
// line, and at most maxOutputBytes of them.
func trimOutput(s string) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > maxOutputLines {
		lines = lines[len(lines)-maxOutputLines:]
	}
	out := strings.Join(lines, "; ")
	if len(out) > maxOutputBytes {
		out = "…" + strings.ToValidUTF8(out[len(out)-maxOutputBytes:], "")
	}
	return out
}

// hints maps markers of common registry failures, matched case-insensitively
// against a command's output, to the action that fixes them. The first match
// wins, so the specific markers come before the generic ones.
var hints = []struct {
	markers []string
	hint    string
}{
	{
		[]string{"repositorynotfoundexception"},
		"the ECR repository does not exist: create it, or fix the repository name in the config",
	},
	{
		[]string{"name_unknown", "repository name not known"},
		"the repository does not exist in the registry: create it, or fix the repository name in the config",
	},
	{
		[]string{"401 unauthorized", "unauthorized:", "authentication required", "no basic auth credentials", "expiredtokenexception", "unrecognizedclientexception"},
		"the registry rejected the credentials: log in to it (docker login, or the cloud provider's login) and check the token has not expired",
	},
	{
		[]string{"403 forbidden", "denied:", "accessdeniedexception", "requested access to the resource is denied"},
		"the registry denied access: check the credentials have permission on this repository",
	},
}

func hintFor(output string) string {
	lower := strings.ToLower(output)
	for _, h := range hints {
		for _, m := range h.markers {
			if strings.Contains(lower, m) {
				return h.hint
			}
		}
	}
	return ""
}
