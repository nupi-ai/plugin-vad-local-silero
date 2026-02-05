//go:build silero

package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// resolveORTLibPath returns the path to the ONNX Runtime shared library.
// Search order:
//  1. NUPI_ORT_LIB_PATH environment variable (explicit override)
//  2. lib/<goos>-<goarch>/ relative to executable
//  3. ../lib/<goos>-<goarch>/ relative to executable (bin/ layout)
//  4. lib/<goos>-<goarch>/ relative to CWD (only if NUPI_DEV_MODE=1)
//  5. ../lib/<goos>-<goarch>/ relative to CWD (only if NUPI_DEV_MODE=1)
//
// CWD-based lookup is disabled by default to prevent shared library hijacking.
// Set NUPI_DEV_MODE=1 during development to enable CWD fallback.
func resolveORTLibPath() (string, error) {
	// 1. Explicit override via environment variable.
	if envPath := os.Getenv("NUPI_ORT_LIB_PATH"); envPath != "" {
		info, err := os.Stat(envPath)
		if err != nil {
			return "", fmt.Errorf("ort: NUPI_ORT_LIB_PATH=%q does not exist", envPath)
		}
		if info.IsDir() {
			return "", fmt.Errorf("ort: NUPI_ORT_LIB_PATH=%q is a directory, expected a file", envPath)
		}
		return envPath, nil
	}

	filename := ortLibFilename()
	libRel := filepath.Join("lib", runtime.GOOS+"-"+runtime.GOARCH, filename)
	libRelParent := filepath.Join("..", "lib", runtime.GOOS+"-"+runtime.GOARCH, filename)

	// 2-3. Try relative to executable location.
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		for _, rel := range []string{libRel, libRelParent} {
			path := filepath.Join(exeDir, rel)
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}
	}

	// 4-5. Fall back to CWD only in dev mode (prevents shared library hijacking).
	if os.Getenv("NUPI_DEV_MODE") == "1" {
		if dir, err := os.Getwd(); err == nil {
			for _, rel := range []string{libRel, libRelParent} {
				path := filepath.Join(dir, rel)
				if _, err := os.Stat(path); err == nil {
					return path, nil
				}
			}
		}
	}

	return "", fmt.Errorf("ort: shared library not found; searched lib/<os>-<arch>/%s relative to executable (set NUPI_ORT_LIB_PATH to override, or NUPI_DEV_MODE=1 to enable CWD lookup)", filename)
}

// ortLibFilename returns the platform-specific ONNX Runtime library filename.
func ortLibFilename() string {
	switch runtime.GOOS {
	case "darwin":
		return "libonnxruntime.dylib"
	case "windows":
		return "onnxruntime.dll"
	default: // linux and others
		return "libonnxruntime.so"
	}
}
