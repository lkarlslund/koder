package browser

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/lkarlslund/koder/internal/fsutil"
)

func (m *Manager) enforceProfilePreferences() error {
	if m == nil {
		return nil
	}
	path := filepath.Join(m.profileDir, "Default", "Preferences")
	preferences := map[string]any{}
	data, err := os.ReadFile(path)
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&preferences); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}

	preferences["credentials_enable_service"] = false
	preferences["credentials_enable_autosignin"] = false
	setPreference(preferences, "translate", "enabled", false)
	setPreference(preferences, "session", "restore_on_startup", json.Number("5"))
	setPreference(preferences, "profile", "password_manager_enabled", false)
	setPreference(preferences, "profile", "password_manager_leak_detection", false)
	setPreference(preferences, "profile", "exit_type", "Normal")
	setPreference(preferences, "profile", "exited_cleanly", true)

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create browser preference directory: %w", err)
	}
	return writeJSONAtomically(path, preferences, 0o600)
}

func setPreference(root map[string]any, section, key string, value any) {
	nested, ok := root[section].(map[string]any)
	if !ok {
		nested = map[string]any{}
		root[section] = nested
	}
	nested[key] = value
}

func writeJSONAtomically(path string, value any, mode fs.FileMode) (err error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, buf.Bytes(), mode)
}
