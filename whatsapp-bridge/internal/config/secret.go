package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Secret returns value, trimmed, unless it is blank; else the contents of
// path; else a random secret (32 bytes as hex) written to path with mode
// 0600 and logged. It resolves Config.Token against store/api_token and
// Webhook.Secret against store/webhook_secret.
//
// The API token is what every API request must present; the MCP server
// reads it from the same file. Without it any local process, or any web
// page making requests to localhost, could send messages as this account.
func Secret(value, path string, log *slog.Logger) (string, error) {
	if secret := strings.TrimSpace(value); secret != "" {
		return secret, nil
	}
	if data, err := os.ReadFile(path); err == nil { //nolint:gosec // the bridge's own store/ file
		if secret := strings.TrimSpace(string(data)); secret != "" {
			return secret, nil
		}
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	secret := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(secret+"\n"), 0600); err != nil {
		return "", fmt.Errorf("failed to write %s: %w", path, err)
	}
	if log != nil {
		log.Info("generated secret", "path", path)
	}
	return secret, nil
}
