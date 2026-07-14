package locations

import (
	"fmt"
	"path/filepath"
)

type Mode string

const (
	ModeUser   Mode = "user"
	ModeDaemon Mode = "daemon"
)

type Paths struct {
	Mode       Mode
	ConfigDir  string
	ConfigFile string
	ModelDir   string
	StateDir   string
	RuntimeDir string
	LogDir     string
	SocketPath string
}

func Resolve(mode Mode, goos, goarch, home string) (Paths, error) {
	switch mode {
	case ModeUser:
		if home == "" {
			return Paths{}, fmt.Errorf("resolve user mode: home directory is required")
		}
		root := filepath.Join(home, "yllmd")
		state := filepath.Join(root, "state")
		return Paths{
			Mode:       mode,
			ConfigDir:  root,
			ConfigFile: filepath.Join(root, "config.yaml"),
			ModelDir:   filepath.Join(root, "models"),
			StateDir:   state,
			RuntimeDir: state,
			LogDir:     filepath.Join(root, "logs"),
			SocketPath: filepath.Join(state, "yllmd.sock"),
		}, nil
	case ModeDaemon:
		return daemonPaths(goos, goarch)
	default:
		return Paths{}, fmt.Errorf("unsupported mode %q (expected user or daemon)", mode)
	}
}

func daemonPaths(goos, goarch string) (Paths, error) {
	var configDir, stateDir, runtimeDir, logDir string
	switch goos {
	case "linux":
		configDir = "/etc/yllmd"
		stateDir = "/var/lib/yllmd"
		runtimeDir = "/var/run/yllmd"
		logDir = "/var/log/yllmd"
	case "freebsd":
		configDir = "/usr/local/etc/yllmd"
		stateDir = "/var/db/yllmd"
		runtimeDir = "/var/run/yllmd"
		logDir = "/var/log/yllmd"
	case "darwin":
		prefix := "/usr/local"
		if goarch == "arm64" {
			prefix = "/opt/homebrew"
		}
		configDir = filepath.Join(prefix, "etc", "yllmd")
		stateDir = filepath.Join(prefix, "var", "lib", "yllmd")
		runtimeDir = filepath.Join(prefix, "var", "run", "yllmd")
		logDir = filepath.Join(prefix, "var", "log", "yllmd")
	default:
		return Paths{}, fmt.Errorf("daemon mode is not supported on %s", goos)
	}
	return Paths{
		Mode:       ModeDaemon,
		ConfigDir:  configDir,
		ConfigFile: filepath.Join(configDir, "config.yaml"),
		ModelDir:   filepath.Join(stateDir, "models"),
		StateDir:   stateDir,
		RuntimeDir: runtimeDir,
		LogDir:     logDir,
		SocketPath: filepath.Join(runtimeDir, "yllmd.sock"),
	}, nil
}
