package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

// DataDir returns the durable application data directory. On Linux it follows
// the XDG base-directory spec: $XDG_DATA_HOME/<app> or ~/.local/share/<app>.
func DataDir(appName string) (string, error) {
	if runtime.GOOS == "linux" {
		if base := os.Getenv("XDG_DATA_HOME"); base != "" {
			return filepath.Join(base, appName), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "share", appName), nil
	}

	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appName), nil
}

func ConfigPath(appName string) (string, error) {
	dir, err := DataDir(appName)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

func DBPath(appName string) (string, error) {
	dir, err := DataDir(appName)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "donnied.db"), nil
}
