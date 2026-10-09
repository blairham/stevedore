// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package run

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// A captured command's error carries its stderr: a missing repository used
// to read as nothing more than "aws: exit status 254".
func TestCaptureErrorCarriesStderr(t *testing.T) {
	_, err := Query(
		t.Context(),
		"sh",
		"-c",
		`echo "An error occurred (RepositoryNotFoundException) when calling the DescribeImages operation: The repository with name 'app' does not exist" >&2; exit 254`,
	)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"exit status 254", "RepositoryNotFoundException", "The repository with name 'app' does not exist", "the ECR repository does not exist"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to contain %q", err, want)
		}
	}
	// Callers still reach the exit error and its stderr.
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 254 || len(ee.Stderr) == 0 {
		t.Errorf("errors.As ExitError = %v", ee)
	}
}

func TestCaptureErrorWithoutStderr(t *testing.T) {
	_, err := Query(t.Context(), "sh", "-c", "exit 3")
	if err == nil || err.Error() != "sh: exit status 3" {
		t.Errorf("err = %v, want the bare exit status", err)
	}
}

func TestHintFor(t *testing.T) {
	cases := map[string]string{
		"An error occurred (RepositoryNotFoundException) when calling":                                "ECR repository does not exist",
		"GET https://ghcr.io/v2/x/app/tags/list: NAME_UNKNOWN: repository name not known to registry": "repository does not exist in the registry",
		"GET https://reg.io/v2/: UNAUTHORIZED: authentication required":                               "rejected the credentials",
		"no basic auth credentials":                          "rejected the credentials",
		"denied: requested access to the resource is denied": "denied access",
		"403 Forbidden":                                      "denied access",
		"error: something else entirely":                     "",
	}
	for out, want := range cases {
		got := hintFor(out)
		if want == "" && got != "" || want != "" && !strings.Contains(got, want) {
			t.Errorf("hintFor(%q) = %q, want %q", out, got, want)
		}
	}
}

func TestTrimOutputKeepsTheTail(t *testing.T) {
	var b strings.Builder
	for i := range 30 {
		b.WriteString(strings.Repeat("x", i) + "\n\n")
	}
	b.WriteString("the reason\n")
	got := trimOutput(b.String())
	if !strings.HasSuffix(got, "the reason") {
		t.Errorf("trimOutput lost the last line: %q", got)
	}
	if n := strings.Count(got, "; ") + 1; n != maxOutputLines {
		t.Errorf("kept %d lines, want %d", n, maxOutputLines)
	}
	long := trimOutput(strings.Repeat("y", 5000))
	if len(long) > maxOutputBytes+len("…") || !strings.HasPrefix(long, "…") {
		t.Errorf("trimOutput did not cap a long line: %d bytes", len(long))
	}
}
