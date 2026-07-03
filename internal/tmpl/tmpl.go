// Package tmpl renders Go text/templates against the release context.
package tmpl

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/blairham/stevedore/internal/gitinfo"
)

// Context is the data available to templates in the config (tags, labels,
// build args). Field names mirror goreleaser where practical.
type Context struct {
	ProjectName string
	Version     string
	Tag         string
	Commit      string
	ShortCommit string
	Branch      string
	Date        string
	Timestamp   int64
	IsSnapshot  bool
	IsDefault   bool // HEAD is on the configured default branch
	Env         map[string]string
}

// NewContext builds a template context from git info and options.
func NewContext(projectName, defaultBranch string, gi *gitinfo.Info, snapshot bool, now time.Time, env map[string]string) *Context {
	return &Context{
		ProjectName: projectName,
		Version:     gi.Version,
		Tag:         gi.Tag,
		Commit:      gi.Commit,
		ShortCommit: gi.ShortCommit,
		Branch:      gi.Branch,
		Date:        now.UTC().Format(time.RFC3339),
		Timestamp:   now.UTC().Unix(),
		IsSnapshot:  snapshot,
		IsDefault:   gi.Branch == defaultBranch,
		Env:         env,
	}
}

// WithVersion returns a shallow copy of the context with Version overridden.
// Used when each image derives its own version (per-image registry versioning).
func (c *Context) WithVersion(version string) *Context {
	clone := *c
	clone.Version = version
	return &clone
}

var funcs = template.FuncMap{
	"lower":      strings.ToLower,
	"upper":      strings.ToUpper,
	"trim":       strings.TrimSpace,
	"replace":    strings.ReplaceAll,
	"trimPrefix": strings.TrimPrefix,
	"trimSuffix": strings.TrimSuffix,
}

// Render evaluates a single template string against ctx.
func Render(s string, ctx *Context) (string, error) {
	t, err := template.New("stevedore").Funcs(funcs).Option("missingkey=error").Parse(s)
	if err != nil {
		return "", fmt.Errorf("parse template %q: %w", s, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
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
