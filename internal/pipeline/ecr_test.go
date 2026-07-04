package pipeline

import (
	"testing"

	"github.com/blairham/stevedore/internal/config"
)

func TestBuildKey(t *testing.T) {
	base := ImagePlan{
		Image:     config.Image{Dockerfile: "Dockerfile", Context: ".", Platforms: []string{"linux/amd64"}},
		BuildArgs: []string{"FOO=bar"},
		Labels:    map[string]string{"k": "v"},
	}
	// Same build spec, different repo/tags -> same key (groups together).
	a := base
	a.Repos = []string{"ghcr.io/x/a"}
	a.Refs = []string{"ghcr.io/x/a:1"}
	b := base
	b.Repos = []string{"ghcr.io/x/b"}
	b.Refs = []string{"ghcr.io/x/b:2"}
	if buildKey("/repo", a) != buildKey("/repo", b) {
		t.Error("identical builds differing only by repo/tag should share a key")
	}

	// Differing build inputs -> different keys.
	diffArg := base
	diffArg.BuildArgs = []string{"FOO=baz"}
	if buildKey("/repo", base) == buildKey("/repo", diffArg) {
		t.Error("different build_args should not group")
	}
	diffTarget := base
	diffTarget.Image.Target = "prod"
	if buildKey("/repo", base) == buildKey("/repo", diffTarget) {
		t.Error("different target should not group")
	}
	diffLabel := base
	diffLabel.Labels = map[string]string{"k": "other"}
	if buildKey("/repo", base) == buildKey("/repo", diffLabel) {
		t.Error("different labels should not group")
	}
}

func TestParseECRRepo(t *testing.T) {
	cases := []struct {
		uri        string
		wantName   string
		wantRegion string
	}{
		{
			"123456789.dkr.ecr.us-east-1.amazonaws.com/acme/checkout",
			"acme/checkout", "us-east-1",
		},
		{
			"123456789.dkr.ecr.eu-west-2.amazonaws.com/team/app/svc",
			"team/app/svc", "eu-west-2",
		},
		{
			// Non-ECR host: name still parsed, region empty.
			"ghcr.io/acme/app",
			"acme/app", "",
		},
		{
			// No slash: whole string is the name.
			"bare-repo",
			"bare-repo", "",
		},
	}
	for _, tc := range cases {
		name, region := parseECRRepo(tc.uri)
		if name != tc.wantName || region != tc.wantRegion {
			t.Errorf("parseECRRepo(%q) = (%q, %q), want (%q, %q)", tc.uri, name, region, tc.wantName, tc.wantRegion)
		}
	}
}

func TestIsRegistryStrategy(t *testing.T) {
	for _, s := range []string{"registry", "ecr"} {
		if !isRegistryStrategy(s) {
			t.Errorf("%q should be a registry strategy", s)
		}
	}
	for _, s := range []string{"git", "static", "env", "command", ""} {
		if isRegistryStrategy(s) {
			t.Errorf("%q should NOT be a registry strategy", s)
		}
	}
}
