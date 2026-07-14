package locations

import "testing"

func TestResolveUserMode(t *testing.T) {
	paths, err := Resolve(ModeUser, "linux", "amd64", "/home/alice")
	if err != nil {
		t.Fatal(err)
	}
	if paths.ConfigFile != "/home/alice/yllmd/config.yaml" {
		t.Fatalf("config file = %q", paths.ConfigFile)
	}
	if paths.ModelDir != "/home/alice/yllmd/models" {
		t.Fatalf("model dir = %q", paths.ModelDir)
	}
	if paths.SocketPath != "/home/alice/yllmd/state/yllmd.sock" {
		t.Fatalf("socket path = %q", paths.SocketPath)
	}
}

func TestResolveDaemonMode(t *testing.T) {
	tests := []struct {
		name       string
		goos       string
		goarch     string
		configFile string
		modelDir   string
	}{
		{"linux", "linux", "amd64", "/etc/yllmd/config.yaml", "/var/lib/yllmd/models"},
		{"freebsd", "freebsd", "amd64", "/usr/local/etc/yllmd/config.yaml", "/var/db/yllmd/models"},
		{"mac arm", "darwin", "arm64", "/opt/homebrew/etc/yllmd/config.yaml", "/opt/homebrew/var/lib/yllmd/models"},
		{"mac intel", "darwin", "amd64", "/usr/local/etc/yllmd/config.yaml", "/usr/local/var/lib/yllmd/models"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			paths, err := Resolve(ModeDaemon, test.goos, test.goarch, "")
			if err != nil {
				t.Fatal(err)
			}
			if paths.ConfigFile != test.configFile {
				t.Fatalf("config file = %q", paths.ConfigFile)
			}
			if paths.ModelDir != test.modelDir {
				t.Fatalf("model dir = %q", paths.ModelDir)
			}
		})
	}
}

func TestResolveRejectsUnsupportedMode(t *testing.T) {
	if _, err := Resolve("desktop", "linux", "amd64", "/home/alice"); err == nil {
		t.Fatal("expected unsupported mode error")
	}
}
