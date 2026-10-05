// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package config defines the stevedore configuration schema and loading/validation.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/blairham/stevedore/internal/tmpl"
)

// DefaultFilenames are the config files stevedore looks for, in order.
var DefaultFilenames = []string{
	".stevedore.yaml",
	".stevedore.yml",
	"stevedore.yaml",
	"stevedore.yml",
}

// Config is the top-level stevedore configuration.
type Config struct {
	// Version is the config schema version. Currently only 1 is supported.
	Version int `yaml:"version"`

	// ProjectName is used as a default for image names and artifact paths.
	ProjectName string `yaml:"project_name"`

	// DefaultBranch is the branch on which "latest"-style floating tags are
	// allowed to publish, and the branch a real (non-snapshot) release must be
	// cut from. Defaults to "main".
	DefaultBranch string `yaml:"default_branch"`

	// PrereleaseFloatingTags lets floating tags ("latest", "*-latest", and
	// major / major.minor tags) publish when the release version is a semver
	// prerelease ("1.3.0-rc.1"). By default they are withheld, so a release
	// candidate never moves "latest" or "1".
	PrereleaseFloatingTags bool `yaml:"prerelease_floating_tags"`

	// DefaultLabels sets org.opencontainers.image.source (the origin remote as
	// https), .revision (the commit), .version (the image's version) and
	// .created (the commit date) on every image. An image's own labels win key
	// by key. On unless set to false.
	DefaultLabels *bool `yaml:"default_labels"`

	// SourceDateEpoch passes SOURCE_DATE_EPOCH — HEAD's commit time — to every
	// build, as a build arg and in buildx's environment, so rebuilding a
	// commit stamps the same timestamps into the image. On unless set to
	// false. A SOURCE_DATE_EPOCH already in the environment, or one set in an
	// image's build_args, wins over the commit time.
	SourceDateEpoch *bool `yaml:"source_date_epoch"`

	// Dist is the output directory for generated artifacts (SBOMs, changelog).
	Dist string `yaml:"dist"`

	// Versioning selects how the release version is derived. Defaults to git.
	Versioning Versioning `yaml:"versioning"`

	// ChangeDetection tunes --only-changed / --changed-since.
	ChangeDetection ChangeDetection `yaml:"change_detection"`

	// ImageDefaults is merged under every entry of Images before anything
	// else reads them: a field an image sets wins, a field it leaves out is
	// taken from here. Maps (labels, annotations) merge key by key, the
	// image's keys winning; lists (tags, platforms, build_args, …) and
	// scalars are replaced whole, never concatenated. It may not set id.
	ImageDefaults Image `yaml:"image_defaults" jsonschema:"without=id"`

	// Cache wires a BuildKit build cache into every image, scoped per image
	// (and per platform on a split leg). An image with its own cache_from or
	// cache_to keeps those instead.
	Cache Cache `yaml:"cache"`

	Images     []Image    `yaml:"images"`
	Sign       Sign       `yaml:"sign"`
	SBOM       SBOM       `yaml:"sbom"`
	Scan       Scan       `yaml:"scan"`
	Test       Test       `yaml:"test"`
	Provenance Provenance `yaml:"provenance"`
	Changelog  Changelog  `yaml:"changelog"`
	Release    Release    `yaml:"release"`
	Announce   Announce   `yaml:"announce"`
	Notify     Notify     `yaml:"notify"`

	// Outputs writes a file from the images a real release pushed, for a
	// GitOps step to pick up (a kustomize images: stanza, Helm values).
	Outputs Outputs `yaml:"outputs"`
	Policy  Policy  `yaml:"policy"`
}

// Policy holds the rules a real (non-snapshot) release is held to.
type Policy struct {
	// Require names the stages a real release may not go without: any of
	// scan, test, sign and sbom. A required stage that is disabled in the
	// config, or skipped with its --skip-* flag, refuses the release.
	Require []string `yaml:"require"`
}

// The stages policy.require can name.
const (
	StageScan = "scan"
	StageTest = "test"
	StageSign = "sign"
	StageSBOM = "sbom"
)

// PolicyStages lists the stages policy.require accepts, in pipeline order.
var PolicyStages = []string{StageScan, StageTest, StageSign, StageSBOM}

// Requires reports whether policy.require names stage.
func (p Policy) Requires(stage string) bool {
	return slices.Contains(p.Require, stage)
}

func (p Policy) validate() error {
	for _, s := range p.Require {
		if !slices.Contains(PolicyStages, s) {
			return fmt.Errorf("policy.require: unknown stage %q (want one of %s)", s, strings.Join(PolicyStages, ", "))
		}
	}
	return nil
}

// Release configures post-build publishing steps.
type Release struct {
	GitHub GitHubRelease `yaml:"github"`
	GitLab GitLabRelease `yaml:"gitlab"`
}

// GitLabRelease configures creating a GitLab release (via the glab CLI) with
// the changelog as the body. Runs only on a real (non-snapshot) release. glab
// authenticates from GITLAB_TOKEN, or in GitLab CI from the job token with
// GLAB_ENABLE_CI_AUTOLOGIN=true.
type GitLabRelease struct {
	Enabled bool `yaml:"enabled"`
}

// AnyEnabled reports whether any release target is enabled.
func (r Release) AnyEnabled() bool { return r.GitHub.Enabled || r.GitLab.Enabled }

// GitHubRelease configures creating a GitHub release (via the gh CLI) with the
// changelog as the body. Runs only on a real (non-snapshot) release.
type GitHubRelease struct {
	Enabled bool `yaml:"enabled"`
	Draft   bool `yaml:"draft"`
	// Prerelease marks the GitHub release as a prerelease. Unset, it follows
	// the version: a semver prerelease ("1.3.0-rc.1") is marked, a release is
	// not. true or false forces it either way.
	Prerelease *bool `yaml:"prerelease"`
}

// Outputs configures the digest-pinned file a release writes for its
// consumers. Template is a Go template over {Project, Version, Images}, where
// each image has ID, Version, Digest, Repository (its first), Ref
// (Repository@Digest), Repositories, DigestRefs (every repository@Digest) and
// Refs (the tagged references). Only images with a pushed digest are listed,
// and the file is written only by a real (not dry-run, not --no-push, not
// split-leg) run that has at least one.
type Outputs struct {
	// File is the path to write, relative to the repository root.
	File string `yaml:"file"`
	// Template renders the file's content.
	Template string `yaml:"template"`
}

func (o Outputs) validate() error {
	if (o.File == "") != (o.Template == "") {
		return fmt.Errorf("outputs.file and outputs.template go together: set both, or neither")
	}
	if o.Template != "" {
		if err := tmpl.Parse(o.Template); err != nil {
			return fmt.Errorf("outputs.template: %w", err)
		}
	}
	return nil
}

// Announce configures release notifications to chat webhooks.
type Announce struct {
	Slack   Webhook `yaml:"slack"`
	Discord Webhook `yaml:"discord"`
}

// Notify configures machine-readable post-push notifications, so a CD system
// can react to newly published image digests. Distinct from announce: notify
// fires once per pushed image with a structured JSON payload, while announce
// posts one human-readable end-of-release message.
type Notify struct {
	Webhook NotifyWebhook `yaml:"webhook"`
}

// NotifyWebhook is a generic webhook that receives one JSON payload per pushed
// image. The URL and any credentials are read from environment variables so
// secrets stay out of the config file.
type NotifyWebhook struct {
	Enabled bool `yaml:"enabled"`
	// URLEnv is the environment variable holding the webhook URL.
	URLEnv string `yaml:"url_env"`
	// BearerEnv names an environment variable whose value is sent as an
	// "Authorization: Bearer <token>" header (optional).
	BearerEnv string `yaml:"bearer_env"`
	// HMACEnv names an environment variable holding a shared secret; when set,
	// the request body is signed with HMAC-SHA256 and the digest is sent as
	// "X-Stevedore-Signature: sha256=<hex>" (optional).
	HMACEnv string `yaml:"hmac_env"`
	// Required, when false, turns a failed delivery (transport error or
	// non-2xx response) into a warning instead of a failed release. Unset means
	// true. Configuration errors — a missing env var, a bad URL, a template
	// that does not render — fail either way.
	Required *bool `yaml:"required"`
	// PayloadTemplate replaces the default JSON body: a Go template over the
	// notification (.Project, .Snapshot, .Image, .Version, .Digest,
	// .Repositories, .Refs) that must render valid JSON. The `json` helper
	// encodes a value, e.g. {"service": {{ json .Image }}}.
	PayloadTemplate string `yaml:"payload_template"`
}

// IsRequired reports whether a failed delivery fails the release.
func (w NotifyWebhook) IsRequired() bool {
	return w.Required == nil || *w.Required
}

// Webhook is a single chat webhook target. The URL is read from an environment
// variable so secrets stay out of the config file.
type Webhook struct {
	Enabled bool `yaml:"enabled"`
	// WebhookEnv is the environment variable holding the webhook URL.
	WebhookEnv string `yaml:"webhook_env"`
	// Template is the message template (Go template over the release context).
	// A sensible default is used when empty.
	Template string `yaml:"template"`
}

// Provenance configures SLSA build provenance attestations emitted by BuildKit
// and pushed alongside the image. Only takes effect when pushing.
type Provenance struct {
	Enabled bool `yaml:"enabled"`
	// Mode is "min" or "max" (the default when provenance is enabled). max
	// records the full build definition (Dockerfile, build args, source)
	// rather than just materials.
	Mode string `yaml:"mode" enum:"min,max"`
}

// ChangeDetection configures which files each image depends on for
// --only-changed and --changed-since. When an image sets Paths, its fingerprint
// and change decision consider only files matching its Paths plus SharedPaths;
// otherwise the whole build context is used.
type ChangeDetection struct {
	// SharedPaths are globs that, when changed, rebuild every image (e.g. the
	// Dockerfile, a shared library, the solution file).
	SharedPaths []string `yaml:"shared_paths"`

	// Resolver auto-derives each image's dependency paths from a project graph
	// instead of hand-written Paths. Currently only "dotnet" (walks .csproj
	// <ProjectReference> transitively). Empty disables auto-resolution.
	Resolver string `yaml:"resolver" enum:"dotnet,"`

	// MarkerRefs, when true, advances a per-image git ref after each successful
	// push and uses it as that image's default change-detection base. An image
	// then rebuilds iff its sources changed since ITS OWN last release —
	// statelessly, with no fingerprint file to persist across CI runs.
	MarkerRefs bool `yaml:"marker_refs"`

	// MarkerPrefix is the ref namespace for markers (default
	// refs/releases/image/). The image id is appended.
	MarkerPrefix string `yaml:"marker_prefix"`
}

// Versioning controls how the release version string is derived.
type Versioning struct {
	// Strategy is one of:
	//   git      derive from git tags (default; the historical behavior)
	//   registry list existing tags via crane, take the highest semver and bump it
	//   ecr      like registry, but list tags via `aws ecr describe-images`
	//            (uses AWS credentials directly — no crane or docker cred helper)
	//   static   use an explicit value
	//   env      read the version from an environment variable
	//   command  run a command and use its stdout as the version
	Strategy string `yaml:"strategy" enum:"git,registry,ecr,static,env,command"`

	// Bump is how much to increment for the registry/ecr strategies:
	// patch (default), minor, or major.
	Bump string `yaml:"bump" enum:"patch,minor,major"`

	// Repo is the repository queried by the registry strategy. Defaults to the
	// first repository of the first image (per-image under multi-image configs).
	Repo string `yaml:"repo"`

	// Region is the AWS region for the ecr strategy. When empty it is inferred
	// from the ECR repository host, then falls back to the aws CLI default.
	Region string `yaml:"region"`

	// Lister is the tool used to list registry tags: "crane" (default). crane
	// authenticates via the docker keychain, so it works with ghcr, Docker Hub,
	// and ECR.
	Lister string `yaml:"lister" enum:"crane"`

	// Initial is the version used by the registry strategy when the repository
	// has no existing semver tags. Defaults to 0.1.0.
	Initial string `yaml:"initial"`

	// Value is the version for the static strategy (may contain templates).
	Value string `yaml:"value"`

	// Env is the environment variable name for the env strategy.
	Env string `yaml:"env"`

	// Command is the shell command whose trimmed stdout is the version for the
	// command strategy, e.g. an ECR-native `aws ecr describe-images` query.
	Command string `yaml:"command"`

	// RequireTag makes a version tag on HEAD the release trigger. Without one,
	// `release` runs as a validate-only build (a snapshot, not pushed); with
	// one, every image builds, whatever change detection would have skipped.
	RequireTag bool `yaml:"require_tag"`
}

// Image describes one buildable image and where it should be published.
type Image struct {
	// ID is a stable identifier for the image, used in logs and artifact names.
	ID string `yaml:"id"`

	Dockerfile string `yaml:"dockerfile"`
	Context    string `yaml:"context"`

	// Target selects a specific stage in a multi-stage Dockerfile (optional).
	Target string `yaml:"target"`

	// Platforms are the target platforms, e.g. linux/amd64, linux/arm64.
	Platforms []string `yaml:"platforms"`

	// BuildArgs are passed as --build-arg. Each entry is "KEY=value" and may
	// contain Go templates, e.g. "VERSION={{ .Version }}".
	BuildArgs []string `yaml:"build_args"`

	// Labels are OCI image labels; values may contain Go templates. They
	// override the default org.opencontainers.image.* labels key by key.
	Labels map[string]string `yaml:"labels"`

	// Annotations are OCI annotations; values may contain Go templates. They
	// are set on the image manifest and, for a multi-platform image, on the
	// index too (a split build's merged list gets them at the index level).
	// Pushed images only: a local --load build carries none.
	Annotations map[string]string `yaml:"annotations"`

	// Secrets are BuildKit build secrets exposed via --secret.
	Secrets []Secret `yaml:"secrets"`

	// Repositories are destination repos without a tag, e.g.
	// ghcr.io/blairham/stevedore. The final references published are the
	// cartesian product of Repositories x Tags.
	Repositories []string `yaml:"repositories"`

	// Tags are tag templates, e.g. "{{ .Version }}", "{{ .ShortCommit }}",
	// "latest". Floating tags like "latest" only publish on the default branch
	// of a non-snapshot release.
	Tags []string `yaml:"tags"`

	// Paths are globs (relative to the repo root) of the files this image
	// depends on, used by --only-changed and --changed-since. Supports ** via
	// doublestar. When empty, change detection falls back to the whole build
	// context. change_detection.shared_paths are always added on top.
	Paths []string `yaml:"paths"`

	// Project points at a project file (e.g. a .csproj) whose dependency graph
	// is walked to derive Paths automatically when change_detection.resolver is
	// set. Relative to the repo root.
	Project string `yaml:"project"`

	// CacheFrom lists buildx --cache-from sources, e.g.
	// "type=registry,ref=ghcr.io/acme/myapp:buildcache". Setting it (or
	// CacheTo) takes the image out of the top-level cache. Values may contain
	// templates; entries that render to an empty string are skipped, so a
	// value like '{{ env "STEVEDORE_CACHE_FROM" }}' enables caching only where
	// the environment provides it (e.g. CI) without breaking local builds.
	CacheFrom []string `yaml:"cache_from"`

	// CacheTo lists buildx --cache-to destinations, e.g.
	// "type=registry,ref=ghcr.io/acme/myapp:buildcache,mode=max". Values may
	// contain templates; empty-rendering entries are skipped (see CacheFrom).
	CacheTo []string `yaml:"cache_to"`

	// ExtraFlags are passed verbatim to `docker buildx build`.
	ExtraFlags []string `yaml:"extra_flags"`
}

// Secret is a BuildKit build secret sourced from an env var or a file.
type Secret struct {
	ID   string `yaml:"id"`
	Env  string `yaml:"env"`
	File string `yaml:"file"`
	// Optional lets a real release build without an env-backed secret whose
	// variable is unset. Without it only a snapshot may: a real release
	// refuses up front rather than failing deep inside the build.
	Optional bool `yaml:"optional"`
}

// EnvName is the environment variable an env-backed secret reads: Env, or the
// ID when Env is unset. It is empty for a file-backed secret.
func (s Secret) EnvName() string {
	if s.File != "" {
		return ""
	}
	if s.Env != "" {
		return s.Env
	}
	return s.ID
}

// Sign configures image signing.
type Sign struct {
	Cosign Cosign `yaml:"cosign"`
}

// Cosign configures cosign signing. When Key is empty, keyless (OIDC) signing
// is used.
type Cosign struct {
	Enabled bool `yaml:"enabled"`
	// Key is the cosign private signing key. It is never used to verify.
	Key string `yaml:"key"`
	// PublicKey is the cosign public key matching Key. `stevedore verify`
	// defaults --key to it; signing ignores it.
	PublicKey string `yaml:"public_key"`
	// Args are extra flags passed to both `cosign sign` and `cosign attest`.
	Args []string `yaml:"args"`
}

// SBOM configures software bill of materials generation.
type SBOM struct {
	Enabled bool `yaml:"enabled"`
	// Generator is the CLI used to produce the SBOM. Only "syft" is supported.
	Generator string `yaml:"generator" enum:"syft"`
	// Format is the syft output format, e.g. spdx-json, cyclonedx-json.
	Format string `yaml:"format"`
	// Attest, when true and cosign signing is enabled, attaches the SBOM as a
	// signed attestation to the pushed image.
	Attest bool `yaml:"attest"`
}

// Test configures a post-build smoke test: the built image is run with Cmd and
// the release is blocked unless it exits with ExpectExit.
type Test struct {
	Enabled bool `yaml:"enabled"`
	// Cmd is the command (and args) to run inside the container. Empty uses the
	// image's default entrypoint/cmd.
	Cmd []string `yaml:"cmd"`
	// ExpectExit is the required exit code (default 0).
	ExpectExit int `yaml:"expect_exit"`
	// Timeout is a Go duration string (e.g. "30s"); empty means 60s.
	Timeout string `yaml:"timeout"`
	// Platforms chooses which of the image's platforms are smoke tested:
	// "native" (default) runs those the docker host runs natively and skips
	// the rest with a warning; "all" also runs the others under emulation
	// (binfmt/QEMU) where the host has an emulator for them. When no
	// configured platform is native, "native" tests under emulation too, so
	// an amd64-only image is still tested on an arm64 host.
	Platforms string `yaml:"platforms" enum:"native,all"`
}

// Test platform modes.
const (
	TestPlatformsNative = "native"
	TestPlatformsAll    = "all"
)

// Scan configures vulnerability scanning of built images. When FailOn is set,
// a release is blocked if any vulnerability at or above that severity is found.
type Scan struct {
	Enabled bool `yaml:"enabled"`
	// Scanner is the CLI used: "grype" (default) or "trivy".
	Scanner string `yaml:"scanner" enum:"grype,trivy"`
	// FailOn is the minimum severity that fails the release:
	// negligible|low|medium|high|critical, or "none" to scan and report
	// without gating. Left unset it defaults to critical; an explicit empty
	// string is read as "none".
	FailOn string `yaml:"fail_on" enum:"negligible,low,medium,high,critical,none,"`
	// Ignore lists vulnerabilities to exclude from the gate. Each entry is a
	// bare ID (e.g. CVE-2023-1234) or an {id, reason, expires} mapping; an
	// entry past its expiry date stops applying and is reported as a warning.
	Ignore []ScanIgnore `yaml:"ignore"`
	// VEX lists OpenVEX/CSAF/CycloneDX VEX documents passed to the scanner as
	// --vex, so statements such as "not_affected" filter its findings.
	VEX []string `yaml:"vex"`
	// Args are extra flags passed verbatim to the scanner. Flags that change
	// its output format or destination are rejected at validation: the gate
	// parses the JSON report, and any other document would read as clean.
	Args []string `yaml:"args"`
}

// ScanIgnoreDateLayout is the format of scan.ignore[].expires.
const ScanIgnoreDateLayout = "2006-01-02"

// ScanIgnore is one scan.ignore entry. In YAML it is either a bare
// vulnerability ID or a mapping that records why the finding is accepted and
// until when, so an ignore is auditable and cannot outlive its justification.
type ScanIgnore struct {
	// ID is the vulnerability ID, matched case-insensitively.
	ID string `yaml:"id"`
	// Reason records why the finding is accepted.
	Reason string `yaml:"reason"`
	// Expires (YYYY-MM-DD) is the last day the ignore applies; from the next
	// day (UTC) the finding counts against the gate again.
	Expires string `yaml:"expires"`
}

// UnmarshalYAML accepts both the bare-ID form and the mapping form. The
// mapping's keys are checked by hand: a custom unmarshaler decodes with a
// fresh decoder that does not inherit KnownFields, and a misspelled "expires"
// must not silently become an ignore that never expires.
func (s *ScanIgnore) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		*s = ScanIgnore{ID: n.Value}
		return nil
	case yaml.MappingNode:
		var out ScanIgnore
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if v.Kind != yaml.ScalarNode {
				return fmt.Errorf("line %d: scan.ignore %s must be a string", v.Line, k.Value)
			}
			switch k.Value {
			case "id":
				out.ID = v.Value
			case "reason":
				out.Reason = v.Value
			case "expires":
				out.Expires = v.Value
			default:
				return fmt.Errorf("line %d: field %s not found in scan.ignore entry (want id, reason, expires)", k.Line, k.Value)
			}
		}
		*s = out
		return nil
	default:
		return fmt.Errorf("line %d: scan.ignore entry must be an ID or an {id, reason, expires} mapping", n.Line)
	}
}

// JSONSchema describes both accepted forms for editors.
func (ScanIgnore) JSONSchema() map[string]any {
	str := func() map[string]any { return map[string]any{"type": "string"} }
	date := str()
	date["pattern"] = `^\d{4}-\d{2}-\d{2}$`
	return map[string]any{
		"oneOf": []any{
			str(),
			map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"id": str(), "reason": str(), "expires": date},
				"required":             []any{"id"},
				"additionalProperties": false,
			},
		},
	}
}

// ExpiresAt returns the instant the ignore stops applying (the start of the
// day after Expires, UTC) and whether it has an expiry at all.
func (s ScanIgnore) ExpiresAt() (time.Time, bool) {
	if s.Expires == "" {
		return time.Time{}, false
	}
	d, err := time.Parse(ScanIgnoreDateLayout, s.Expires)
	if err != nil {
		return time.Time{}, false
	}
	return d.AddDate(0, 0, 1), true
}

// Changelog configures release-note generation from conventional commits.
type Changelog struct {
	Enabled bool `yaml:"enabled"`
	// Sort is "asc" or "desc" (default asc).
	Sort string `yaml:"sort" enum:"asc,desc"`
	// Exclude is a list of regexes; matching commit subjects are dropped.
	Exclude []string `yaml:"exclude"`
	// DependencyDiff, when true (and SBOMs are enabled), appends a section
	// listing packages added/removed/upgraded since the previous release,
	// computed by diffing this release's SBOM against the previous tag's.
	DependencyDiff bool `yaml:"dependency_diff"`
}

// Load reads and parses the config at path, applying defaults.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	// The strict decode above has checked every field, image_defaults
	// included, against the file as written (so its line numbers are the
	// user's); the merged document is decoded again only to pick up the
	// merge.
	doc, merged, err := mergeImageDefaults(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if merged {
		c = Config{}
		if err := doc.Decode(&c); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	if err := c.markExplicitEmpty(data); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	c.applyDefaults()
	return &c, nil
}

// FailOnNone is the scan.fail_on value that scans and reports without gating.
const FailOnNone = "none"

// markExplicitEmpty rewrites settings whose explicit empty value means
// something other than "unset" before applyDefaults can fill them. A plain
// string cannot tell `fail_on: ""` from an absent key, and the default for an
// absent key (critical) is the opposite of what the empty value documents
// (report only).
func (c *Config) markExplicitEmpty(data []byte) error {
	var raw struct {
		Scan struct {
			FailOn *string `yaml:"fail_on"`
		} `yaml:"scan"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Scan.FailOn != nil && *raw.Scan.FailOn == "" {
		c.Scan.FailOn = FailOnNone
	}
	return nil
}

// ErrNoConfig is returned by Discover when dir holds none of DefaultFilenames.
var ErrNoConfig = errors.New("no stevedore config found")

// Discover finds the first existing default config file in dir.
func Discover(dir string) (string, error) {
	for _, name := range DefaultFilenames {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w (looked for %v)", ErrNoConfig, DefaultFilenames)
}

func (c *Config) applyDefaults() {
	if c.Version == 0 {
		c.Version = 1
	}
	orDefault(&c.DefaultBranch, "main")
	orDefault(&c.Dist, "dist")
	orDefault(&c.SBOM.Generator, "syft")
	orDefault(&c.SBOM.Format, "spdx-json")
	orDefault(&c.Changelog.Sort, "asc")
	if c.Provenance.Enabled {
		orDefault(&c.Provenance.Mode, "max")
	}
	if c.ChangeDetection.MarkerRefs {
		orDefault(&c.ChangeDetection.MarkerPrefix, "refs/releases/image/")
	}
	orDefault(&c.Versioning.Strategy, "git")
	orDefault(&c.Versioning.Bump, "patch")
	orDefault(&c.Versioning.Lister, "crane")
	orDefault(&c.Versioning.Initial, "0.1.0")
	if c.Scan.Enabled {
		orDefault(&c.Scan.Scanner, "grype")
		// Secure-by-default: block on criticals unless told otherwise
		// ("none", or an explicit "", opts out).
		orDefault(&c.Scan.FailOn, "critical")
	}
	for i := range c.Images {
		c.Images[i].applyDefaults(i, c.ProjectName)
	}
}

// applyDefaults fills an image's unset fields. The i-th image with no id takes
// the project name, or "image<i>" when there is none.
func (img *Image) applyDefaults(i int, projectName string) {
	orDefault(&img.ID, projectName)
	orDefault(&img.ID, fmt.Sprintf("image%d", i))
	orDefault(&img.Dockerfile, "Dockerfile")
	orDefault(&img.Context, ".")
	if len(img.Platforms) == 0 {
		img.Platforms = []string{"linux/amd64"}
	}
	if len(img.Tags) == 0 {
		img.Tags = []string{"{{ .Version }}"}
	}
}

// orDefault sets *field to value when it is empty.
func orDefault(field *string, value string) {
	if *field == "" {
		*field = value
	}
}

// DefaultLabelsEnabled reports whether the default OCI labels are set
// (default_labels unset or true).
func (c *Config) DefaultLabelsEnabled() bool {
	return c.DefaultLabels == nil || *c.DefaultLabels
}

// SourceDateEpochEnabled reports whether SOURCE_DATE_EPOCH is passed to builds
// (source_date_epoch unset or true).
func (c *Config) SourceDateEpochEnabled() bool {
	return c.SourceDateEpoch == nil || *c.SourceDateEpoch
}

// Validate checks the config for internal consistency. It runs on the merged
// images, so a field image_defaults supplies counts as the image's own.
func (c *Config) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported config version %d (want 1)", c.Version)
	}
	if c.ImageDefaults.ID != "" {
		return fmt.Errorf("image_defaults.id: an id names one image and cannot be a default")
	}
	if err := c.validateImages(); err != nil {
		return err
	}
	if c.Sign.Cosign.Key != "" {
		if _, err := os.Stat(c.Sign.Cosign.Key); err != nil {
			return fmt.Errorf("sign.cosign.key %q not readable: %w", c.Sign.Cosign.Key, err)
		}
	}
	if c.Sign.Cosign.PublicKey != "" {
		if _, err := os.Stat(c.Sign.Cosign.PublicKey); err != nil {
			return fmt.Errorf("sign.cosign.public_key %q not readable: %w", c.Sign.Cosign.PublicKey, err)
		}
	}
	if err := c.Scan.validate(); err != nil {
		return err
	}
	if err := c.Cache.validate(); err != nil {
		return err
	}
	if err := c.Provenance.validate(); err != nil {
		return err
	}
	if err := c.Policy.validate(); err != nil {
		return err
	}
	if c.Notify.Webhook.Enabled && c.Notify.Webhook.URLEnv == "" {
		return fmt.Errorf("notify.webhook.enabled requires notify.webhook.url_env")
	}
	if err := c.Outputs.validate(); err != nil {
		return err
	}
	if t := c.Notify.Webhook.PayloadTemplate; t != "" {
		if err := tmpl.Parse(t); err != nil {
			return fmt.Errorf("notify.webhook.payload_template: %w", err)
		}
	}
	switch c.Test.Platforms {
	case "", TestPlatformsNative, TestPlatformsAll:
	default:
		return fmt.Errorf("test.platforms %q invalid (want %s or %s)", c.Test.Platforms, TestPlatformsNative, TestPlatformsAll)
	}
	if c.Test.Enabled && c.Test.Timeout != "" {
		if _, err := time.ParseDuration(c.Test.Timeout); err != nil {
			return fmt.Errorf("test.timeout %q invalid: %w", c.Test.Timeout, err)
		}
	}
	// Enum-like strings are matched exactly where they are used, so a value
	// outside the set does not fail: it silently picks a branch. "descending"
	// sorted desc only because it is not "asc"; "Dotnet" turned the resolver
	// off and under-scoped every image.
	switch c.Changelog.Sort {
	case "", "asc", "desc":
	default:
		return fmt.Errorf("changelog.sort %q invalid (want asc or desc)", c.Changelog.Sort)
	}
	switch c.ChangeDetection.Resolver {
	case "", "dotnet":
	default:
		return fmt.Errorf("change_detection.resolver %q unsupported (want dotnet, or empty to disable)", c.ChangeDetection.Resolver)
	}
	return c.Versioning.validate()
}

func (c *Config) validateImages() error {
	if len(c.Images) == 0 {
		return fmt.Errorf("no images defined")
	}
	seen := map[string]bool{}
	for i, img := range c.Images {
		if seen[img.ID] {
			return fmt.Errorf("images[%d]: duplicate id %q", i, img.ID)
		}
		seen[img.ID] = true
		if len(img.Repositories) == 0 {
			return fmt.Errorf("images[%d] (%s): at least one repository is required", i, img.ID)
		}
		for _, s := range img.Secrets {
			if s.ID == "" {
				return fmt.Errorf("images[%d] (%s): secret with empty id", i, img.ID)
			}
			if s.Env == "" && s.File == "" {
				return fmt.Errorf("images[%d] (%s): secret %q needs env or file", i, img.ID, s.ID)
			}
		}
	}
	return nil
}

func (s Scan) validate() error {
	if !s.Enabled {
		return nil
	}
	switch s.Scanner {
	case "grype", "trivy":
	default:
		return fmt.Errorf("scan.scanner %q unsupported (want grype or trivy)", s.Scanner)
	}
	if !ValidSeverity(s.FailOn) {
		return fmt.Errorf("scan.fail_on %q invalid (want one of: %s, %s)", s.FailOn, strings.Join(Severities, ", "), FailOnNone)
	}
	for i, ig := range s.Ignore {
		if strings.TrimSpace(ig.ID) == "" {
			return fmt.Errorf("scan.ignore[%d]: id is required", i)
		}
		if ig.Expires != "" {
			if _, err := time.Parse(ScanIgnoreDateLayout, ig.Expires); err != nil {
				return fmt.Errorf("scan.ignore[%d] (%s): expires %q is not a YYYY-MM-DD date", i, ig.ID, ig.Expires)
			}
		}
	}
	for _, v := range s.VEX {
		if _, err := os.Stat(v); err != nil {
			return fmt.Errorf("scan.vex %q not readable: %w", v, err)
		}
	}
	for _, a := range s.Args {
		if flag := outputFlag(s.Scanner, a); flag != "" {
			return fmt.Errorf("scan.args: %q changes the %s output format or destination, which stevedore must control to read the JSON report; remove it (the raw JSON report is saved under dist/)", flag, s.Scanner)
		}
	}
	return nil
}

// scanOutputFlags are, per scanner, the flags that change what the scanner
// prints. stevedore appends scan.args after its own "--format json" / "-o json",
// and the last one wins, so any of these would hand the parser a document it
// cannot gate on.
var scanOutputFlags = map[string][]string{
	"trivy": {"-f", "--format", "-o", "--output", "-t", "--template"},
	"grype": {"-o", "--output", "--file", "-t", "--template"},
}

// outputFlag reports which output-changing flag arg is ("" when none),
// recognizing "--flag value", "--flag=value", and the attached short form
// "-ojson".
func outputFlag(scanner, arg string) string {
	for _, f := range scanOutputFlags[scanner] {
		switch {
		case arg == f, strings.HasPrefix(arg, f+"="):
			return f
		case len(f) == 2 && strings.HasPrefix(arg, f):
			return f
		}
	}
	return ""
}

// Cache is the top-level build cache. stevedore renders the buildx
// --cache-from/--cache-to entries itself, with a scope per image — and per
// platform on a split leg, so the legs of one image do not evict each other.
type Cache struct {
	// Type is gha (the GitHub Actions cache), registry (a cache image per
	// scope, tagged <ref>:<scope>), local (a directory per scope,
	// <ref>/<scope>) or none. Empty is none.
	Type string `yaml:"type" enum:"gha,registry,local,none"`
	// Ref is where the cache lives: a repository for registry, a directory
	// for local. Not used by gha. May contain templates.
	Ref string `yaml:"ref"`
	// Mode is the cache-to mode, min or max (default max: cache every
	// intermediate layer, which is what makes a multi-stage build cheap).
	Mode string `yaml:"mode" enum:"min,max"`
}

// Cache types.
const (
	CacheGHA      = "gha"
	CacheRegistry = "registry"
	CacheLocal    = "local"
	CacheNone     = "none"
)

// Enabled reports whether the cache is configured to do anything.
func (c Cache) Enabled() bool { return c.Type != "" && c.Type != CacheNone }

func (c Cache) validate() error {
	switch c.Type {
	case "", CacheNone:
		if c.Ref != "" || c.Mode != "" {
			return fmt.Errorf("cache.ref and cache.mode need a cache.type")
		}
		return nil
	case CacheGHA:
		if c.Ref != "" {
			return fmt.Errorf("cache.ref is not used by type gha (the scope is per image); remove it")
		}
	case CacheRegistry, CacheLocal:
		if c.Ref == "" {
			return fmt.Errorf("cache.type %s needs cache.ref (a %s)", c.Type, map[string]string{CacheRegistry: "repository", CacheLocal: "directory"}[c.Type])
		}
	default:
		return fmt.Errorf("cache.type %q unsupported (want gha, registry, local or none)", c.Type)
	}
	switch c.Mode {
	case "", "min", "max":
		return nil
	default:
		return fmt.Errorf("cache.mode %q invalid (want min or max)", c.Mode)
	}
}

func (p Provenance) validate() error {
	if !p.Enabled {
		return nil
	}
	switch p.Mode {
	case "", "min", "max":
		return nil
	default:
		return fmt.Errorf("provenance.mode %q invalid (want min or max)", p.Mode)
	}
}

// validate checks the versioning strategy and its required fields.
func (v Versioning) validate() error {
	// initial becomes the version verbatim when the registry has no semver tags
	// yet, so it must be one the next run can parse and bump.
	if v.Initial != "" && !isStableSemver(v.Initial) {
		return fmt.Errorf("versioning.initial %q invalid (want MAJOR.MINOR.PATCH, optionally with a leading v)", v.Initial)
	}
	switch v.Strategy {
	case "", "git":
	case "registry", "ecr":
		switch v.Bump {
		case "", "patch", "minor", "major":
		default:
			return fmt.Errorf("versioning.bump %q invalid (want patch, minor, or major)", v.Bump)
		}
		if v.Strategy == "registry" && v.Lister != "" && v.Lister != "crane" {
			return fmt.Errorf("versioning.lister %q unsupported (only crane)", v.Lister)
		}
	case "static":
		if v.Value == "" {
			return fmt.Errorf("versioning.strategy=static requires versioning.value")
		}
	case "env":
		if v.Env == "" {
			return fmt.Errorf("versioning.strategy=env requires versioning.env")
		}
	case "command":
		if v.Command == "" {
			return fmt.Errorf("versioning.strategy=command requires versioning.command")
		}
	default:
		return fmt.Errorf("versioning.strategy %q unsupported (want git, registry, ecr, static, env, or command)", v.Strategy)
	}
	return nil
}

// isStableSemver reports whether s is an optional "v" followed by exactly three
// dot-separated non-negative integers. It accepts what the versioner's
// parseSemver does; config cannot import the versioner, which imports config.
func isStableSemver(s string) bool {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if n, err := strconv.Atoi(p); err != nil || n < 0 {
			return false
		}
	}
	return true
}

// Severities are the recognized vulnerability severities, ascending.
var Severities = []string{"negligible", "low", "medium", "high", "critical"}

// ValidSeverity reports whether s is a recognized scan.fail_on value: a
// severity, or "none" (and empty, its in-memory equivalent) to scan and report
// without gating.
func ValidSeverity(s string) bool {
	if s == "" || s == FailOnNone {
		return true
	}
	return slices.Contains(Severities, s)
}
