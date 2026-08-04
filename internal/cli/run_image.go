package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/novusedge/stoat/internal/core"
)

// runImages lists what stoat can build from: the catalog plus anything else
// under isos/, with an exact size for what is downloaded and the catalog's
// declared approximation for what is not: the distinction core.CatalogImage
// carries, surfaced rather than flattened.
func runImages(a *Args, stdout, stderr io.Writer) int {
	imgs, err := core.Images()
	if err != nil {
		fmt.Fprintln(stderr, "stoat: images:", err)
		return ExitFail
	}
	fmt.Fprintf(stdout, "%-16s %-9s %-11s %-10s %s\n", "ID", "OS", "VARIANT", "SIZE", "STATE")
	for _, i := range imgs {
		size := humanSize(i.Bytes)
		if !i.Exact {
			size = "~" + size
		}
		state := "not downloaded"
		if i.Downloaded {
			state = "downloaded"
		}
		id := i.ID
		if id == "" {
			id = i.File // a byo file has no catalog id
			state = "byo"
		}
		fmt.Fprintf(stdout, "%-16s %-9s %-11s %-10s %s\n", id, i.OS, i.Variant, size, state)
	}
	return ExitOK
}

// runPull downloads a catalog image. ^C cancels it for real now: the ctx
// reaches the HTTP body read, which is why iso.Download needed a stall timeout
// before and why an abandoned download used to keep running.
func runPull(a *Args, stdout, stderr io.Writer) int {
	var lastPct int = -1
	progress := func(done, total int64) {
		if a.Quiet || total <= 0 {
			return
		}
		if pct := int(done * 100 / total); pct != lastPct {
			lastPct = pct
			fmt.Fprintf(stdout, "\r%s  %3d%%  %s / %s", a.VM, pct, humanSize(done), humanSize(total))
		}
	}
	res, err := core.DownloadImage(context.Background(), a.VM, progress)
	if err != nil {
		fmt.Fprintln(stderr, "\nstoat: pull:", err)
		return ExitFail
	}
	if !a.Quiet {
		// Say so when nothing checked the bytes. This image gets booted.
		note := ""
		if !res.ChecksumAvailable {
			note = ": UNVERIFIED (no published checksum)"
		}
		fmt.Fprintf(stdout, "\r%s downloaded%s%s\n", a.VM, note, strings.Repeat(" ", 30))
	}
	return ExitOK
}

// humanSize renders bytes for a listing. Deliberately tiny and local: the TUI
// has its own renderer tuned to its column widths, and sharing one would mean
// the CLI's format changing whenever the TUI's layout did.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
