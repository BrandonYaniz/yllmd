//go:build linux

package machine

import (
	"strings"
	"testing"
)

func TestParseLinuxMeminfo(t *testing.T) {
	memory, err := parseLinuxMeminfo(strings.NewReader("MemFree: 20 kB\nMemTotal: 16384 kB\n"))
	if err != nil {
		t.Fatal(err)
	}
	if memory != 16384*1024 {
		t.Fatalf("memory = %d", memory)
	}
}
