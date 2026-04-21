package storage

import "testing"

func TestHashPasswordAndCheckPasswordHash(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword() returned error: %v", err)
	}

	if hash == "" {
		t.Fatal("HashPassword() returned an empty hash")
	}

	if !CheckPasswordHash("correct horse battery staple", hash) {
		t.Fatal("CheckPasswordHash() should accept the original password")
	}

	if CheckPasswordHash("wrong password", hash) {
		t.Fatal("CheckPasswordHash() should reject a different password")
	}
}

func TestGetEnvReturnsDefaultWhenUnset(t *testing.T) {
	t.Setenv("PAIRLINE_TEST_GETENV", "")

	if got := getEnv("PAIRLINE_TEST_GETENV", "fallback"); got != "fallback" {
		t.Fatalf("getEnv() = %q, want %q", got, "fallback")
	}
}

func TestGetEnvReturnsConfiguredValue(t *testing.T) {
	t.Setenv("PAIRLINE_TEST_GETENV", "configured")

	if got := getEnv("PAIRLINE_TEST_GETENV", "fallback"); got != "configured" {
		t.Fatalf("getEnv() = %q, want %q", got, "configured")
	}
}

func TestGetEnvAsIntHandlesValidAndInvalidValues(t *testing.T) {
	t.Setenv("PAIRLINE_TEST_GETENV_INT", "42")
	if got := getEnvAsInt("PAIRLINE_TEST_GETENV_INT", 7); got != 42 {
		t.Fatalf("getEnvAsInt(valid) = %d, want %d", got, 42)
	}

	t.Setenv("PAIRLINE_TEST_GETENV_INT", "not-a-number")
	if got := getEnvAsInt("PAIRLINE_TEST_GETENV_INT", 7); got != 7 {
		t.Fatalf("getEnvAsInt(invalid) = %d, want %d", got, 7)
	}

	t.Setenv("PAIRLINE_TEST_GETENV_INT", "")
	if got := getEnvAsInt("PAIRLINE_TEST_GETENV_INT", 7); got != 7 {
		t.Fatalf("getEnvAsInt(empty) = %d, want %d", got, 7)
	}
}

func TestDatabaseHelpersAreNilSafe(t *testing.T) {
	var db *Database

	if got := db.GetDB(); got != nil {
		t.Fatalf("GetDB() = %#v, want nil", got)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close() returned error: %v", err)
	}

	if err := RunWithStartupMigrationLock(nil, nil); err != nil {
		t.Fatalf("RunWithStartupMigrationLock(nil, nil) returned error: %v", err)
	}
}
