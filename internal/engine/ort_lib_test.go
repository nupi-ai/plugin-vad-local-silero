//go:build silero

package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// NOTE: The primary production lookup path (lib/<os>-<arch>/ relative to executable)
// is NOT directly tested here because it requires controlling the test binary's
// location on the filesystem, which is complex and fragile across CI environments.
//
// The executable-relative resolution is indirectly validated by:
// 1. Integration tests that run the actual binary with ORT in the expected location
// 2. The make test-silero target which exercises the full path
//
// Tests below cover: env override, CWD fallback (dev mode), and error cases.

func TestResolveORTLibPath_EnvOverride(t *testing.T) {
	// Create a temp file to use as a fake ORT library.
	tmpFile, err := os.CreateTemp("", "fake_ort_*.so")
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	// Set env override and verify it's returned.
	t.Setenv("NUPI_ORT_LIB_PATH", tmpFile.Name())
	t.Setenv("NUPI_DEV_MODE", "") // ensure dev mode is off

	path, err := resolveORTLibPath()
	if err != nil {
		t.Fatalf("resolveORTLibPath failed: %v", err)
	}
	if path != tmpFile.Name() {
		t.Errorf("expected %q, got %q", tmpFile.Name(), path)
	}
}

func TestResolveORTLibPath_EnvOverrideMissing(t *testing.T) {
	// Point to non-existent file.
	t.Setenv("NUPI_ORT_LIB_PATH", "/nonexistent/path/to/ort.so")
	t.Setenv("NUPI_DEV_MODE", "")

	_, err := resolveORTLibPath()
	if err == nil {
		t.Fatal("expected error for non-existent NUPI_ORT_LIB_PATH")
	}
}

func TestResolveORTLibPath_EnvOverrideIsDirectory(t *testing.T) {
	// Point to a directory instead of a file.
	tmpDir, err := os.MkdirTemp("", "ort_dir_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	t.Setenv("NUPI_ORT_LIB_PATH", tmpDir)
	t.Setenv("NUPI_DEV_MODE", "")

	_, err = resolveORTLibPath()
	if err == nil {
		t.Fatal("expected error when NUPI_ORT_LIB_PATH is a directory")
	}
}

func TestResolveORTLibPath_CwdFallbackDevMode(t *testing.T) {
	// Create temp dir structure: lib/<os>-<arch>/libonnxruntime.*
	tmpDir, err := os.MkdirTemp("", "ort_cwd_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	libDir := filepath.Join(tmpDir, "lib", runtime.GOOS+"-"+runtime.GOARCH)
	if err := os.MkdirAll(libDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create fake library file.
	libPath := filepath.Join(libDir, ortLibFilename())
	if err := os.WriteFile(libPath, []byte("fake"), 0644); err != nil {
		t.Fatal(err)
	}

	// Change to temp dir and enable dev mode.
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	t.Setenv("NUPI_ORT_LIB_PATH", "") // no override
	t.Setenv("NUPI_DEV_MODE", "1")

	path, err := resolveORTLibPath()
	if err != nil {
		t.Fatalf("resolveORTLibPath failed in dev mode with CWD lib: %v", err)
	}
	// Normalize paths to handle symlinks (e.g., /var vs /private/var on macOS).
	absPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}
	absLibPath, err := filepath.EvalSymlinks(libPath)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", libPath, err)
	}
	if absPath != absLibPath {
		t.Errorf("expected %q, got %q", absLibPath, absPath)
	}
}

func TestResolveORTLibPath_CwdIgnoredWithoutDevMode(t *testing.T) {
	// Create temp dir structure with lib, but without dev mode it should NOT be used.
	tmpDir, err := os.MkdirTemp("", "ort_nodev_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	libDir := filepath.Join(tmpDir, "lib", runtime.GOOS+"-"+runtime.GOARCH)
	if err := os.MkdirAll(libDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create fake library file.
	libPath := filepath.Join(libDir, ortLibFilename())
	if err := os.WriteFile(libPath, []byte("fake"), 0644); err != nil {
		t.Fatal(err)
	}

	// Change to temp dir but do NOT enable dev mode.
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	t.Setenv("NUPI_ORT_LIB_PATH", "") // no override
	t.Setenv("NUPI_DEV_MODE", "")     // dev mode OFF

	// Should fail because CWD lookup is disabled without dev mode,
	// and executable-relative path won't find our temp lib.
	path, err := resolveORTLibPath()
	if err == nil {
		// ORT was found via executable-relative path (e.g., during make test-silero).
		// Verify the returned path is NOT the CWD-based path — that's the whole
		// point of this test: CWD must be ignored without dev mode.
		absCwdLib, evalErr := filepath.EvalSymlinks(libPath)
		if evalErr != nil {
			t.Fatalf("EvalSymlinks(%q): %v", libPath, evalErr)
		}
		absResolved, evalErr := filepath.EvalSymlinks(path)
		if evalErr != nil {
			t.Fatalf("EvalSymlinks(%q): %v", path, evalErr)
		}
		if absResolved == absCwdLib {
			t.Errorf("resolveORTLibPath returned CWD path %q without dev mode — CWD fallback should be disabled", path)
		}
	}
}

// TestOrtLibFilename is in silero_test.go
