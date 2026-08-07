package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/core"
)

// runImages lists what stoat can build from: the catalog plus anything else
// under isos/, with an exact size for what is downloaded and the catalog's
// declared approximation for what is not: the distinction core.CatalogImage
// carries, surfaced rather than flattened.
func runImages(a *Args, stdout, stderr io.Writer) int {
	imgs, err := core.Images()
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		return a.ok(stdout, map[string]any{"images": wire.FromCatalogImages(imgs)})
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

// runPull downloads a catalog image. ^C cancels it: the ctx reaches the
// HTTP body read, so an abandoned download stops instead of running to
// completion in the background.
func runPull(a *Args, stdout, stderr io.Writer) int {
	var em *wire.Emitter
	if a.JSON {
		em = wire.NewEmitter(stdout)
	}
	var lastPct int = -1
	// Both renderers fire only on a percentage CHANGE: a per-read event is
	// thousands of lines for one image, and a consumer gains nothing from them.
	progress := func(done, total int64) {
		if total <= 0 {
			return
		}
		pct := int(done * 100 / total)
		if pct == lastPct {
			return
		}
		lastPct = pct
		switch {
		case em != nil:
			_ = em.Event(wire.TypeProgress, a.Cmd, map[string]any{
				"id": a.VM, "done": done, "total": total, "percent": pct,
			})
		case !a.Quiet:
			fmt.Fprintf(stdout, "\r%s  %3d%%  %s / %s", a.VM, pct, humanSize(done), humanSize(total))
		}
	}
	res, err := core.DownloadImage(context.Background(), a.VM, progress)
	if err != nil {
		if a.JSON {
			return a.fail(stdout, stderr, err)
		}
		fmt.Fprintln(stderr, "\nstoat: pull:", err)
		return ExitFail
	}
	if a.JSON {
		// The DTO carries verified/checksum_available: "downloaded but nothing
		// checked the bytes" is a fact a caller about to boot the image needs.
		d := wire.FromDownloadResult(res)
		return a.ok(stdout, map[string]any{
			"id":                 a.VM,
			"downloaded":         true,
			"verified":           d.Verified,
			"checksum_available": d.ChecksumAvailable,
		})
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
