package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeEnv writes a .env file in a temp dir and chdirs into it so the
// relative path used by loadDotEnv resolves there. t.Chdir restores the
// original working directory at the end of the test.
func writeEnv(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
}

func TestLoadDotEnvMultilineKey(t *testing.T) {
	key := "-----BEGIN RSA PRIVATE KEY-----\nLINE1\nLINE2\n-----END RSA PRIVATE KEY-----"
	writeEnv(t, "# comment\nGITHUB_APP_SLUG=quisac\nGITHUB_APP_PRIVATE_KEY=\""+key+"\"\nPORT=9000\n")

	for _, k := range []string{"GITHUB_APP_SLUG", "GITHUB_APP_PRIVATE_KEY", "PORT"} {
		os.Unsetenv(k)
		t.Cleanup(func() { os.Unsetenv(k) })
	}

	loadDotEnv(".env")

	if got := os.Getenv("GITHUB_APP_SLUG"); got != "quisac" {
		t.Errorf("slug = %q, want quisac", got)
	}
	if got := os.Getenv("GITHUB_APP_PRIVATE_KEY"); got != key {
		t.Errorf("private key did not round-trip:\n got %q\nwant %q", got, key)
	}
	if got := os.Getenv("GITHUB_APP_PRIVATE_KEY"); !strings.HasPrefix(got, "-----BEGIN") {
		t.Errorf("private key lost its PEM header: %q", got)
	}
}

func TestLoadDotEnvDoesNotOverrideRealEnv(t *testing.T) {
	writeEnv(t, "GITHUB_APP_SLUG=from-file\n")

	os.Setenv("GITHUB_APP_SLUG", "from-real-env")
	t.Cleanup(func() { os.Unsetenv("GITHUB_APP_SLUG") })

	loadDotEnv(".env")

	if got := os.Getenv("GITHUB_APP_SLUG"); got != "from-real-env" {
		t.Errorf("real env should win: got %q, want from-real-env", got)
	}
}
