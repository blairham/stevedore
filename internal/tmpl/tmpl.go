// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package tmpl renders Go text/templates against the release context.
package tmpl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
	"text/template/parse"
	"time"

	"github.com/blairham/stevedore/internal/gitinfo"
	"github.com/blairham/stevedore/internal/semver"
)

// Context is the data available to templates in the config (tags, labels,
// build args). Field names mirror goreleaser where practical.
type Context struct {
	ProjectName string
	// ID is the id of the image being rendered; empty outside one (an
	// announcement, the release-wide versioning repo).
	ID          string
	Version     string
	Tag         string // the tag on HEAD; empty on an untagged commit
	LatestTag   string // the most recent tag reachable from HEAD (Tag when HEAD is tagged)
	Commit      string
	ShortCommit string
	Branch      string
	Date        string // wall-clock build time; differs on every build
	Timestamp   int64
	// CommitDate / CommitTimestamp are HEAD's committer time, the same on
	// every build of a commit — the reproducible alternative to Date. Empty
	// and 0 in a repository with no commits.
	CommitDate      string
	CommitTimestamp int64
	// SourceURL is the origin remote as an https URL, "" without one.
	SourceURL  string
	IsSnapshot bool
	IsDefault  bool // HEAD is on the configured default branch
	Detached   bool // HEAD points at a commit, not a branch (a tag checkout)
	Env        map[string]string
}

// NewContext builds a template context from git info and options.
func NewContext(projectName, defaultBranch string, gi *gitinfo.Info, snapshot bool, now time.Time, env map[string]string) *Context {
	c := &Context{
		ProjectName: projectName,
		Version:     gi.Version,
		Tag:         gi.Tag,
		LatestTag:   gi.LatestTag,
		Commit:      gi.Commit,
		ShortCommit: gi.ShortCommit,
		Branch:      gi.Branch,
		Date:        now.UTC().Format(time.RFC3339),
		Timestamp:   now.UTC().Unix(),
		IsSnapshot:  snapshot,
		// Not a string compare against gi.Branch: a tag-triggered release runs
		// on a detached HEAD, where that is the literal "HEAD" and never equals
		// the default branch — which silently withheld every floating tag from
		// every real release.
		IsDefault: gi.OnBranch(defaultBranch),
		Detached:  gi.Detached,
		SourceURL: gi.SourceURL,
		Env:       env,
	}
	if !gi.CommitTime.IsZero() {
		c.CommitDate = gi.CommitTime.UTC().Format(time.RFC3339)
		c.CommitTimestamp = gi.CommitTime.Unix()
	}
	return c
}

// WithImage returns a shallow copy of the context for rendering image id's
// fields, so {{ .ID }} names it.
func (c *Context) WithImage(id string) *Context {
	clone := *c
	clone.ID = id
	return &clone
}

// WithVersion returns a shallow copy of the context with Version overridden.
// Used when each image derives its own version (per-image registry versioning).
func (c *Context) WithVersion(version string) *Context {
	clone := *c
	clone.Version = version
	return &clone
}

// The semver parts of Version are methods rather than fields so they can never
// drift from it (WithVersion, and contexts built by hand, need not set them)
// and so a template asking for one of a version that is not semver fails
// instead of rendering "0".

// Major is the MAJOR part of Version ("1" for 1.2.3).
func (c *Context) Major() (int, error) {
	v, err := c.semver()
	return v.Major, err
}

// Minor is the MINOR part of Version ("2" for 1.2.3).
func (c *Context) Minor() (int, error) {
	v, err := c.semver()
	return v.Minor, err
}

// Patch is the PATCH part of Version ("3" for 1.2.3).
func (c *Context) Patch() (int, error) {
	v, err := c.semver()
	return v.Patch, err
}

// Prerelease is the prerelease part of Version without its "-" ("rc.1" for
// 1.3.0-rc.1), or "" for a release.
func (c *Context) Prerelease() (string, error) {
	v, err := c.semver()
	return v.Prerelease(), err
}

// IsPrerelease reports whether Version names a prerelease ("1.3.0-rc.1"). A
// snapshot version is never one — "1.4.0-SNAPSHOT-9f8e7d6" is a build after
// a release, not a release candidate; .IsSnapshot says that — and neither is
// a version that is not semver at all (a static "2024.10"). Asking is never an
// error, so it is safe in an {{ if }}.
func (c *Context) IsPrerelease() bool {
	if _, snap := gitinfo.IsSnapshotVersion(c.Version); snap {
		return false
	}
	v, ok := semver.Parse(c.Version)
	return ok && v.IsPrerelease()
}

// semver parses Version. A snapshot version is read as the version it was
// built on: its "-SNAPSHOT-<sha>" suffix is stevedore's, not a prerelease the
// user chose, so {{ .Prerelease }} of 1.4.0-SNAPSHOT-9f8e7d6 is empty.
func (c *Context) semver() (semver.Version, error) {
	s := c.Version
	if base, snap := gitinfo.IsSnapshotVersion(s); snap {
		s = base
	}
	v, ok := semver.Parse(s)
	if !ok {
		return semver.Version{}, fmt.Errorf("version %q is not a semantic version (MAJOR.MINOR.PATCH)", c.Version)
	}
	return v, nil
}

// Fields returns the top-level context fields (and methods) a template refers
// to — "Major" for {{ .Major }} or {{ $.Major }}, "Env" for {{ .Env.HOME }}.
// A field reached inside {{ with }} or {{ range }} is reported too, since dot
// is not tracked; that only ever over-reports.
func Fields(s string) (map[string]bool, error) {
	t, err := template.New("stevedore").Funcs(funcs).Parse(s)
	if err != nil {
		return nil, fmt.Errorf("parse template %q: %w", s, err)
	}
	found := map[string]bool{}
	if t.Tree != nil {
		walkFields(t.Root, found)
	}
	return found, nil
}

func walkFields(n parse.Node, found map[string]bool) {
	switch n := n.(type) {
	case *parse.FieldNode:
		found[n.Ident[0]] = true
	case *parse.VariableNode:
		// $.Major: the root variable, then a field path.
		if len(n.Ident) > 1 && n.Ident[0] == "$" {
			found[n.Ident[1]] = true
		}
	default:
		for _, c := range children(n) {
			walkFields(c, found)
		}
	}
}

// children lists the nodes under n that can hold a field reference.
func children(n parse.Node) []parse.Node {
	switch n := n.(type) {
	case *parse.ListNode:
		if n != nil {
			return n.Nodes
		}
	case *parse.ActionNode:
		return []parse.Node{n.Pipe}
	case *parse.PipeNode:
		if n != nil {
			out := make([]parse.Node, len(n.Cmds))
			for i, c := range n.Cmds {
				out[i] = c
			}
			return out
		}
	case *parse.CommandNode:
		return n.Args
	case *parse.ChainNode:
		return []parse.Node{n.Node}
	case *parse.IfNode:
		return branch(&n.BranchNode)
	case *parse.RangeNode:
		return branch(&n.BranchNode)
	case *parse.WithNode:
		return branch(&n.BranchNode)
	}
	return nil
}

func branch(b *parse.BranchNode) []parse.Node {
	return []parse.Node{b.Pipe, b.List, b.ElseList}
}

var funcs = template.FuncMap{
	"lower":      strings.ToLower,
	"upper":      strings.ToUpper,
	"trim":       strings.TrimSpace,
	"replace":    strings.ReplaceAll,
	"trimPrefix": strings.TrimPrefix,
	"trimSuffix": strings.TrimSuffix,
	"json":       toJSON,
}

// toJSON encodes v as JSON, so a template that builds a JSON document (the
// notify payload) can place any value without hand-quoting it.
func toJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

// Render evaluates a single template string against ctx.
func Render(s string, ctx *Context) (string, error) {
	return RenderData(s, ctx)
}

// Parse reports whether s is a syntactically valid template using the helper
// functions, so a config error surfaces at load rather than mid-release.
func Parse(s string) error {
	_, err := template.New("stevedore").Funcs(funcs).Parse(s)
	return err
}

// RenderData evaluates a template string against arbitrary data, with the same
// helper functions and missingkey=error as Render. It is for templates whose
// data is not the release context, such as the notify payload.
func RenderData(s string, data any) (string, error) {
	t, err := template.New("stevedore").Funcs(funcs).Option("missingkey=error").Parse(s)
	if err != nil {
		return "", fmt.Errorf("parse template %q: %w", s, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render template %q: %w", s, err)
	}
	return buf.String(), nil
}

// RenderAll renders each string in the slice.
func RenderAll(in []string, ctx *Context) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, s := range in {
		r, err := Render(s, ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
