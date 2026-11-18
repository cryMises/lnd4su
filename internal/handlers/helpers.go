package handlers

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/crymises/lnd4su/internal/auth"
)

func verifyPassword(e *auth.Entry, password string) bool { return auth.Verify(e, password) }
func adminFlag(e *auth.Entry) bool                       { return auth.IsAdmin(e) }
func newSessionToken() string                            { return auth.NewToken() }

func openData(root string, parts ...string) (*os.File, error) {
	return os.Open(filepath.Join(append([]string{root}, parts...)...))
}

func readData(root string, parts ...string) ([]byte, error) {
	return os.ReadFile(filepath.Join(append([]string{root}, parts...)...))
}

func writeData(root string, parts []string, data []byte) error {
	p := filepath.Join(append([]string{root}, parts...)...)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

func removeData(root string, parts ...string) {
	_ = os.Remove(filepath.Join(append([]string{root}, parts...)...))
}

// readAllLimit reads at most limit bytes and fails if there is more.
func readAllLimit(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("too large")
	}
	return data, nil
}
