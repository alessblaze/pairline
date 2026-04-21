package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnvIfEnabledLoadsEnvFile(t *testing.T) {
	const key = "PAIRLINE_TEST_DOTENV_VALUE"

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() returned error: %v", err)
	}

	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("Chdir() returned error: %v", err)
	}
	defer func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore Chdir() returned error: %v", err)
		}
	}()

	originalValue, hadOriginal := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("Unsetenv() returned error: %v", err)
	}
	defer func() {
		if hadOriginal {
			_ = os.Setenv(key, originalValue)
			return
		}
		_ = os.Unsetenv(key)
	}()

	t.Setenv(SkipDotEnvEnvVar, "")

	envPath := filepath.Join(tempDir, ".env")
	if err := os.WriteFile(envPath, []byte(key+"=loaded-from-dotenv\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	LoadDotEnvIfEnabled()

	if got := os.Getenv(key); got != "loaded-from-dotenv" {
		t.Fatalf("LoadDotEnvIfEnabled() loaded %q, want %q", got, "loaded-from-dotenv")
	}
}

func TestLoadDotEnvIfEnabledSkipsWhenConfigured(t *testing.T) {
	const key = "PAIRLINE_TEST_DOTENV_SKIPPED"

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() returned error: %v", err)
	}

	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("Chdir() returned error: %v", err)
	}
	defer func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore Chdir() returned error: %v", err)
		}
	}()

	originalValue, hadOriginal := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("Unsetenv() returned error: %v", err)
	}
	defer func() {
		if hadOriginal {
			_ = os.Setenv(key, originalValue)
			return
		}
		_ = os.Unsetenv(key)
	}()

	t.Setenv(SkipDotEnvEnvVar, "1")

	envPath := filepath.Join(tempDir, ".env")
	if err := os.WriteFile(envPath, []byte(key+"=should-not-load\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}

	LoadDotEnvIfEnabled()

	if got := os.Getenv(key); got != "" {
		t.Fatalf("LoadDotEnvIfEnabled() loaded %q, want empty value", got)
	}
}
