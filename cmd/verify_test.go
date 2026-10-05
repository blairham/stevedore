// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"strings"
	"testing"

	"github.com/blairham/stevedore/internal/config"
)

func TestVerifyKeyNeverUsesThePrivateKey(t *testing.T) {
	keyed := func(pub string) *config.Config {
		c := &config.Config{}
		c.Sign.Cosign.Key = "cosign.key"
		c.Sign.Cosign.PublicKey = pub
		return c
	}
	cases := []struct {
		name    string
		flag    string
		keyless bool
		cfg     *config.Config
		want    string
		wantErr string
	}{
		{name: "no config", want: ""},
		{name: "keyless config", cfg: &config.Config{}, want: ""},
		{name: "public key from config", cfg: keyed("cosign.pub"), want: "cosign.pub"},
		{name: "flag wins", flag: "other.pub", cfg: keyed("cosign.pub"), want: "other.pub"},
		{name: "identity flags mean keyless", keyless: true, cfg: keyed("cosign.pub"), want: ""},
		{name: "private key only", cfg: keyed(""), wantErr: "sign.cosign.public_key"},
		{name: "private key only, explicit flag", flag: "cosign.pub", cfg: keyed(""), want: "cosign.pub"},
		{name: "private key only, keyless flags", keyless: true, cfg: keyed(""), want: ""},
	}
	for _, c := range cases {
		got, err := verifyKey(c.flag, c.keyless, c.cfg)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%s: want error mentioning %q, got %q, %v", c.name, c.wantErr, got, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: verifyKey = %q, %v; want %q", c.name, got, err, c.want)
		}
		if got == "cosign.key" {
			t.Errorf("%s: the private key was chosen to verify with", c.name)
		}
	}
}
