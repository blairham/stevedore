// Package config defines the stevedore configuration schema and loading/validation.
package config

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
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
	// allowed to publish. Defaults to "main".
	DefaultBranch string `yaml:"default_branch"`

	// Dist is the output directory for generated artifacts (SBOMs, changelog).
	Dist string `yaml:"dist"`

	// Versioning selects how the release version is derived. Defaults to git.
	Versioning Versioning `yaml:"versioning"`

	// ChangeDetection tunes --only-changed / --changed-since.
	ChangeDetection ChangeDetection `yaml:"change_detection"`

	Images     []Image    `yaml:"images"`
	Sign       Sign       `yaml:"sign"`
	SBOM       SBOM       `yaml:"sbom"`
	Scan       Scan       `yaml:"scan"`
	Test       Test       `yaml:"test"`
	Provenance Provenance `yaml:"provenance"`
	Changelog  Changelog  `yaml:"changelog"`
	Release    Release    `yaml:"release"`
	Announce   Announce   `yaml:"announce"`
}

// Release configures post-build publishing steps.
type Release struct {
	GitHub GitHubRelease `yaml:"github"`
}

// GitHubRelease configures creating a GitHub release (via the gh CLI) with the
// changelog as the body. Runs only on a real (non-snapshot) release.
type GitHubRelease struct {
	Enabled    bool `yaml:"enabled"`
	Draft      bool `yaml:"draft"`
	Prerelease bool `yaml:"prerelease"`
}

// Announce configures release notifications to chat webhooks.
type Announce struct {
	Slack   Webhook `yaml:"slack"`
	Discord Webhook `yaml:"discord"`
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
	// Mode is "min" (default) or "max". max records the full build definition
	// (Dockerfile, build args, source) rather than just materials.
	Mode string `yaml:"mode"`
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
	Resolver string `yaml:"resolver"`

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
	Strategy string `yaml:"strategy"`

	// Bump is how much to increment for the registry/ecr strategies:
	// patch (default), minor, or major.
	Bump string `yaml:"bump"`

	// Repo is the repository queried by the registry strategy. Defaults to the
	// first repository of the first image (per-image under multi-image configs).
	Repo string `yaml:"repo"`

	// Region is the AWS region for the ecr strategy. When empty it is inferred
	// from the ECR repository host, then falls back to the aws CLI default.
	Region string `yaml:"region"`

	// Lister is the tool used to list registry tags: "crane" (default). crane
	// authenticates via the docker keychain, so it works with ghcr, Docker Hub,
	// and ECR.
	Lister string `yaml:"lister"`

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

	// Labels are OCI image labels; values may contain Go templates.
	Labels map[string]string `yaml:"labels"`

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
	// "type=registry,ref=ghcr.io/acme/myapp:buildcache". Values may contain
	// templates; entries that render to an empty string are skipped, so a
	// value like '{{ index .Env "STEVEDORE_CACHE_FROM" }}' enables caching only where
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
}

// Sign configures image signing.
type Sign struct {
	Cosign Cosign `yaml:"cosign"`
}

// Cosign configures cosign signing. When Key is empty, keyless (OIDC) signing
// is used.
type Cosign struct {
	Enabled bool     `yaml:"enabled"`
	Key     string   `yaml:"key"`
	Args    []string `yaml:"args"`
}

// SBOM configures software bill of materials generation.
type SBOM struct {
	Enabled bool `yaml:"enabled"`
	// Generator is the CLI used to produce the SBOM. Only "syft" is supported.
	Generator string `yaml:"generator"`
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
}

// Scan configures vulnerability scanning of built images. When FailOn is set,
// a release is blocked if any vulnerability at or above that severity is found.
type Scan struct {
	Enabled bool `yaml:"enabled"`
	// Scanner is the CLI used: "grype" (default) or "trivy".
	Scanner string `yaml:"scanner"`
	// FailOn is the minimum severity that fails the release:
	// negligible|low|medium|high|critical. Empty means scan-and-report without
	// gating.
	FailOn string `yaml:"fail_on"`
	// Ignore lists vulnerability IDs (e.g. CVE-2023-1234) to exclude from the
	// gate.
	Ignore []string `yaml:"ignore"`
	// Args are extra flags passed verbatim to the scanner.
	Args []string `yaml:"args"`
}

// Changelog configures release-note generation from conventional commits.
type Changelog struct {
	Enabled bool `yaml:"enabled"`
	// Sort is "asc" or "desc" (default asc).
	Sort string `yaml:"sort"`
	// Exclude is a list of regexes; matching commit subjects are dropped.
	Exclude []string `yaml:"exclude"`
	// DependencyDiff, when true (and SBOMs are enabled), appends a section
	// listing packages added/removed/upgraded since the previous release,
	// computed by diffing this release's SBOM against the previous tag's.
	DependencyDiff bool `yaml:"dependency_diff"`
}

// Load reads and parses the config at path, applying defaults.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	c.applyDefaults()
	return &c, nil
}

// Discover finds the first existing default config file in dir.
func Discover(dir string) (string, error) {
	for _, name := range DefaultFilenames {
		p := dir + string(os.PathSeparator) + name
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no stevedore config found (looked for %v)", DefaultFilenames)
}

func (c *Config) applyDefaults() {
	if c.Version == 0 {
		c.Version = 1
	}
	if c.DefaultBranch == "" {
		c.DefaultBranch = "main"
	}
	if c.Dist == "" {
		c.Dist = "dist"
	}
	if c.SBOM.Generator == "" {
		c.SBOM.Generator = "syft"
	}
	if c.SBOM.Format == "" {
		c.SBOM.Format = "spdx-json"
	}
	if c.Changelog.Sort == "" {
		c.Changelog.Sort = "asc"
	}
	if c.Provenance.Enabled && c.Provenance.Mode == "" {
		c.Provenance.Mode = "max"
	}
	if c.ChangeDetection.MarkerRefs && c.ChangeDetection.MarkerPrefix == "" {
		c.ChangeDetection.MarkerPrefix = "refs/releases/image/"
	}
	if c.Versioning.Strategy == "" {
		c.Versioning.Strategy = "git"
	}
	if c.Versioning.Bump == "" {
		c.Versioning.Bump = "patch"
	}
	if c.Versioning.Lister == "" {
		c.Versioning.Lister = "crane"
	}
	if c.Versioning.Initial == "" {
		c.Versioning.Initial = "0.1.0"
	}
	if c.Scan.Enabled {
		if c.Scan.Scanner == "" {
			c.Scan.Scanner = "grype"
		}
		if c.Scan.FailOn == "" {
			// Secure-by-default: block on criticals unless told otherwise.
			c.Scan.FailOn = "critical"
		}
	}
	for i := range c.Images {
		img := &c.Images[i]
		if img.ID == "" {
			img.ID = c.ProjectName
			if img.ID == "" {
				img.ID = fmt.Sprintf("image%d", i)
			}
		}
		if img.Dockerfile == "" {
			img.Dockerfile = "Dockerfile"
		}
		if img.Context == "" {
			img.Context = "."
		}
		if len(img.Platforms) == 0 {
			img.Platforms = []string{"linux/amd64"}
		}
		if len(img.Tags) == 0 {
			img.Tags = []string{"{{ .Version }}"}
		}
	}
}

// Validate checks the config for internal consistency.
func (c *Config) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported config version %d (want 1)", c.Version)
	}
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
	if c.Sign.Cosign.Key != "" {
		if _, err := os.Stat(c.Sign.Cosign.Key); err != nil {
			return fmt.Errorf("sign.cosign.key %q not readable: %w", c.Sign.Cosign.Key, err)
		}
	}
	if c.Scan.Enabled {
		switch c.Scan.Scanner {
		case "grype", "trivy":
		default:
			return fmt.Errorf("scan.scanner %q unsupported (want grype or trivy)", c.Scan.Scanner)
		}
		if !ValidSeverity(c.Scan.FailOn) {
			return fmt.Errorf("scan.fail_on %q invalid (want one of: %s)", c.Scan.FailOn, strings.Join(Severities, ", "))
		}
	}
	if c.Provenance.Enabled {
		switch c.Provenance.Mode {
		case "", "min", "max":
		default:
			return fmt.Errorf("provenance.mode %q invalid (want min or max)", c.Provenance.Mode)
		}
	}
	if c.Test.Enabled && c.Test.Timeout != "" {
		if _, err := time.ParseDuration(c.Test.Timeout); err != nil {
			return fmt.Errorf("test.timeout %q invalid: %w", c.Test.Timeout, err)
		}
	}
	if err := c.Versioning.validate(); err != nil {
		return err
	}
	return nil
}

// validate checks the versioning strategy and its required fields.
func (v Versioning) validate() error {
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

// Severities are the recognized vulnerability severities, ascending.
var Severities = []string{"negligible", "low", "medium", "high", "critical"}

// ValidSeverity reports whether s is a recognized severity (empty is allowed:
// scan-and-report without gating).
func ValidSeverity(s string) bool {
	if s == "" {
		return true
	}
	return slices.Contains(Severities, s)
}
