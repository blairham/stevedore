package pipeline

import "testing"

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
