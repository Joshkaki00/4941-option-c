package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnvMissingIsOK(t *testing.T) {
	if err := LoadDotEnv(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDotEnvSetsUnsetOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "# comment\nANTHROPIC_API_KEY=sk-from-file\nLISTEN_ADDR=:9090\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("LISTEN_ADDR", ":1111") // must win over file
	if err := os.Unsetenv("ANTHROPIC_API_KEY"); err != nil {
		t.Fatal(err)
	}

	if err := LoadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("ANTHROPIC_API_KEY"); got != "sk-from-file" {
		t.Fatalf("ANTHROPIC_API_KEY=%q", got)
	}
	if got := os.Getenv("LISTEN_ADDR"); got != ":1111" {
		t.Fatalf("LISTEN_ADDR should keep existing value, got %q", got)
	}
}
