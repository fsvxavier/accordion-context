package launcher

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// tempFileBase names the temp config file inside a run's temp dir.
const tempFileBase = "client-config"

// WriteTempConfig writes the temp config of t into dir, which the caller
// creates as a private 0700 directory and removes when the run ends. The
// content is the rendered template, or the user's merge_from file with the
// base URL set at t.Key. The file is created with mode 0600 and never
// overwrites an existing file. It returns the file path.
func WriteTempConfig(dir string, t TempConfig, baseURL, port, token string) (string, error) {
	content, err := tempContent(t, baseURL, port, token)
	if err != nil {
		return "", fmt.Errorf("temp_config: %w", err)
	}
	path := filepath.Join(dir, tempFileBase+ConfigExt(t.Format))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("temp config: %w", err)
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("temp config: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("temp config: %w", err)
	}
	return path, nil
}

// tempContent returns the file content: the rendered template, or the user's
// merge_from file with the base URL set at key.
func tempContent(t TempConfig, baseURL, port, token string) (string, error) {
	if t.Template != "" {
		return RenderTempConfig(t, baseURL, port, token)
	}
	src, err := readMergeSource(t.MergeFrom)
	if err != nil {
		return "", err
	}
	out, err := MergeConfig(t.Format, src, t.Key, baseURL)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// readMergeSource reads the user's file. It is the only access to that file:
// nothing is ever written to it.
func readMergeSource(path string) ([]byte, error) {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("merge_from: %w", err)
		}
		path = filepath.Join(home, rest)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("merge_from: %w", err)
	}
	return b, nil
}

// RandomToken returns 32 random bytes as unpadded base64url. It is used for
// the per-run {token} placeholder and for proxy nonces.
func RandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
