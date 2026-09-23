package main

import (
	"strings"
	"testing"

	"github.com/amitgupta1000/WebSearch_GO/internal/config"
)

func TestRedactedTargetNeverLeaksCredentials(t *testing.T) {
	cases := map[string]string{
		"redis://:sup3rsecret@cache.internal:6380": "cache.internal:6380",
		"rediss://user:secret@example.com:1234/2":  "example.com:1234",
		"not a url": "(configured)",
		"":          "(configured)",
	}
	for input, want := range cases {
		got := redactedTarget(input)
		if got != want {
			t.Errorf("redactedTarget(%q) = %q, want %q", input, got, want)
		}
		if strings.Contains(got, "secret") {
			t.Errorf("redactedTarget(%q) leaked the password: %q", input, got)
		}
	}
}

func TestCredentialSourceNamesTheIdentityInUse(t *testing.T) {
	// This string goes into the startup log, so it must say which identity the
	// Google libraries will use.
	if got := credentialSource(&config.Config{}); !strings.Contains(got, "ambient") {
		t.Errorf("credentialSource() = %q, want it to mention the ambient identity", got)
	}
	withFile := credentialSource(&config.Config{GoogleCredentialsFile: "/secrets/cache-sa.json"})
	if !strings.Contains(withFile, "GOOGLE_APPLICATION_CREDENTIALS") {
		t.Errorf("credentialSource() = %q, want it to name GOOGLE_APPLICATION_CREDENTIALS", withFile)
	}
}
