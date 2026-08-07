package tui

import (
	"io"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/novusedge/stoat/internal/core"
)

// logPager is a scrolling view of a VM's console log, opened by detail's
// "L" key (see updateDetail). It replaces the detail screen's body while
// open, rather than overlaying it. app.go's compositor overlay
// (renderModal) is wired specifically to *imageModal, and this file may
// not touch app.go, so a second modal kind there is out of scope. The
// state, resize, and esc-to-close shape still follows imagemodal.go's
// pattern, just rendered inline by viewDetail instead of composited.
type logPager struct {
	vp viewport.Model
	// truncated records whether the log was larger than consoleTailBytes.
	// The footer uses it to say the pager shows a tail, so the user does
	// not assume they are looking at the whole log.
	truncated bool
	// scrolledToBottom becomes true once the initial scroll-to-the-end has
	// run. It must wait for the first real resize. The viewport is built
	// at a placeholder 1x1 size, since the terminal size is not known yet.
	// GotoBottom at that size would compute against soft-wrapped lines of
	// one character each, an offset that overshoots once the real width
	// rewraps the same content into far fewer lines, and the pager renders
	// blank.
	scrolledToBottom bool
}

// consoleTailBytes bounds how much of console.log a pager load reads. A
// long-running VM's console log has no size limit. tailReadCloser seeks
// near the end of it, when the reader supports seeking (see there), rather
// than reading the whole file into memory just to show the last
// screenful.
const consoleTailBytes = 512 * 1024

// logOpenedMsg carries a freshly built pager back to updateDetail. Reading
// and tailing the log happens in openLogPager's Cmd, off the UI goroutine
// like every other blocking call in this package (typeConsolePassword,
// startProvision). It does not happen in the key handler itself.
type logOpenedMsg struct{ pager *logPager }

// openLogPager reads VM name's console log via core.Logs and returns a Cmd
// that produces a logOpenedMsg, or an errMsg on failure. core.Logs already
// handles a VM that has never started, returning an empty reader rather
// than an error, and a broken vm.toml, serving the directory's console.log
// regardless. Neither case needs special handling here.
func openLogPager(name string) tea.Cmd {
	return func() tea.Msg {
		rc, err := core.Logs(name, core.WhichConsole)
		if err != nil {
			return errMsg(err.Error())
		}
		content, truncated, err := tailReadCloser(rc, consoleTailBytes)
		if err != nil {
			return errMsg(err.Error())
		}
		if content == "" {
			content = "(console log is empty)"
		}
		vp := viewport.New(viewport.WithWidth(1), viewport.WithHeight(1))
		vp.SoftWrap = true
		vp.SetContent(content)
		// Scrolling to the bottom (the newest output is what a "show me the
		// console" press wants first) waits for resize to give the viewport
		// its real width; see scrolledToBottom's doc.
		return logOpenedMsg{&logPager{vp: vp, truncated: truncated}}
	}
}

// tailReadCloser closes rc and returns up to the last max bytes written to
// it. rc is core.Logs' *os.File in the common case, which is seekable, so
// this seeks near the end rather than reading forward from the start. A
// console log that has been growing for hours can be many times max, and
// there is no reason to hold all of it in memory just to show the tail.
// Only one case falls through to the plain ReadAll below: the
// io.NopCloser-over-an-empty-reader core.Logs returns for a VM with no
// console log yet. That one is empty, so reading it whole is not a
// problem.
func tailReadCloser(rc io.ReadCloser, max int64) (content string, truncated bool, err error) {
	defer rc.Close()
	if seeker, ok := rc.(io.Seeker); ok {
		if size, serr := seeker.Seek(0, io.SeekEnd); serr == nil {
			start := int64(0)
			if size > max {
				start = size - max
				truncated = true
			}
			if _, serr := seeker.Seek(start, io.SeekStart); serr == nil {
				b, rerr := io.ReadAll(rc)
				if rerr != nil {
					return "", false, rerr
				}
				return string(b), truncated, nil
			}
		}
	}
	b, rerr := io.ReadAll(rc)
	if rerr != nil {
		return "", false, rerr
	}
	return string(b), false, nil
}

// logPagerChrome is the vertical space the pane spends on its border,
// title, and the hint/truncation lines below the viewport, mirroring what
// modalHeightChrome does for the image modal.
const logPagerChrome = 6

// logPagerMargin keeps the pane off the terminal's edges, so it reads as a
// panel over the screen rather than a full-bleed takeover. imagemodal.go's
// width/height fractions make the same call for the image picker.
const logPagerMargin = 4

// resize fits the pager to the terminal. It is called every render, from
// viewDetail, rather than on tea.WindowSizeMsg: this file cannot add a
// message route in app.go. SetWidth and SetHeight are no-ops on an
// unchanged value, so calling resize unconditionally each frame is cheap.
func (p *logPager) resize(termWidth, termHeight int) {
	w := termWidth - paneFrame() - logPagerMargin
	if w < 1 {
		w = 1
	}
	h := termHeight - logPagerChrome
	if h < 1 {
		h = 1
	}
	p.vp.SetWidth(w)
	p.vp.SetHeight(h)
	if !p.scrolledToBottom {
		p.vp.GotoBottom()
		p.scrolledToBottom = true
	}
}

// view renders the pane. width is the pane's outer max width, same
// convention as pane() everywhere else in this package.
func (p *logPager) view(width int) string {
	hint := dimStyle.Render("↑/↓ pgup/pgdown scroll · esc close")
	if p.truncated {
		hint = warnStyle.Render("showing the end of a larger log") + "\n" + hint
	}
	body := p.vp.View() + "\n\n" + hint
	return pane("console log", body, width)
}

// update handles one message while the pager is open. esc is handled by the
// caller (updateDetail), which closes the pager outright; everything else
// goes to the viewport, whose default keymap already covers arrows, j/k,
// and page up/down.
func (p *logPager) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	p.vp, cmd = p.vp.Update(msg)
	return cmd
}
