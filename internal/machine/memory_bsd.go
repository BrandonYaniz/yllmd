//go:build darwin || freebsd

package machine

import (
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

func physicalMemory() (uint64, error) {
	key := "hw.physmem"
	if runtime.GOOS == "darwin" {
		key = "hw.memsize"
	}
	output, err := exec.Command("sysctl", "-n", key).Output()
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return value, nil
}
