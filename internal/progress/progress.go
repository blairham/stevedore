// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package progress writes the human-readable lines a run prints as it goes.
//
// Progress output is best effort. By the time most of these lines are written
// images have already been pushed, and a stdout that has gone away (a closed
// pipe, a full disk behind a redirect) must not abort a release halfway and
// leave the registry ahead of what the run reports. Output that is the
// command's result, such as `--output json`, is not progress and is checked
// where it is written.
package progress

import (
	"fmt"
	"io"
)

// Printf writes a formatted progress line to w, ignoring a write error.
func Printf(w io.Writer, format string, a ...any) {
	fmt.Fprintf(w, format, a...) //nolint:errcheck // best-effort progress output; see the package doc
}

// Println writes a progress line to w, ignoring a write error.
func Println(w io.Writer, a ...any) {
	fmt.Fprintln(w, a...) //nolint:errcheck // best-effort progress output; see the package doc
}
