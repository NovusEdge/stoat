//go:build linux

package hostcheck

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// AvailableMB reports the memory a new process can claim without swapping, in
// MiB. It returns 0 when the host does not say.
//
// MemAvailable is the kernel's own estimate and counts the reclaimable page
// cache, so it stays honest on a host that has been up for weeks. MemFree
// alone would read as almost nothing there and refuse every start.
func AvailableMB() int {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if !strings.HasPrefix(line, "MemAvailable:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.Atoi(fields[1])
		if err != nil {
			return 0
		}
		return kb / 1024
	}
	return 0
}
