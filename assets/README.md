# Screenshots and recording

These captures show Stoat built from commit `7f985bc` on Linux x86-64 with
QEMU 11.1.1. They were captured on 2026-09-06 in Europe/Helsinki time.
The guests use a separate `STOAT_HOME` and cached Alpine 3.24.1, Debian 13,
and Ubuntu 24.04 images.

| File | Content |
|---|---|
| [tui-list.png](tui-list.png) | Main TUI in Konsole; two running VMs and one stopped VM |
| [tui-details.png](tui-details.png) | Alpine VM details and the completed XFCE apply log |
| [tui-create.png](tui-create.png) | New-VM form with a cached Alpine image |
| [qemu-xfce.png](qemu-xfce.png) | Actual QEMU window running Alpine XFCE |
| [alpine-xfce.png](alpine-xfce.png) | Earlier guest display captured through QMP with `stoat screenshot` |
| [demo.mp4](demo.mp4) | 23-second H.264 screen recording, 1280 × 1000, 15 fps, no audio |
| [demo.gif](demo.gif) | Animated copy of the screen recording, 10 fps |

## Capture method

The TUI images and video capture pixels from a Konsole window on KDE.
Konsole used its default color scheme, a 12-point monospace font, and an
opaque background. `NO_COLOR` was unset in the capture process. The main
image uses a 1280 × 768 window; the details and form use 1280 × 1000 so
the full content and key hints fit.

FFmpeg captured the Konsole and QEMU Xwayland windows with `x11grab` and
`-draw_mouse 0`. The TUI hides its text cursor in the list and details views.
The guest terminal's text cursor was hidden for its demonstration command.
The QEMU clip has padding above and below to match the terminal clip's height.
The PNG files are window captures; `alpine-xfce.png` is a direct QMP capture.

The video spends five seconds on the VM list, nine on VM details, and nine
on QEMU. The guest terminal opens during the QEMU segment and prints the
Alpine and kernel versions. The details show `xfce (applied unknown)`: the
recipe completed, and the bundled XFCE recipe has no health check.

## Reproduce the guests

Install the [host prerequisites](../docs/getting-started/installation.md),
then create a separate data root:

```sh
export STOAT_HOME="$(mktemp -d "${TMPDIR:-/tmp}/stoat-docs.XXXXXX")"
stoat pull alpine-standard
stoat pull debian-13
stoat pull ubuntu-24.04

stoat create alpine-desktop --image alpine-standard --ram 2048 --cpus 2 --recipes xfce
stoat create debian-dev --image debian-13 --ram 2048 --cpus 2
stoat create ubuntu-lab --image ubuntu-24.04 --ram 2048 --cpus 2

STOAT_GRAPHICAL=1 stoat up alpine-desktop --json
STOAT_GRAPHICAL=0 stoat up debian-dev
stoat wait debian-dev --until reachable --timeout 2m
stoat exec debian-dev -- cat /etc/debian_version
```

Alpine opens a QEMU window and applies XFCE on its first start. Ubuntu stays
stopped. For the window capture, `xrandr` was installed in Alpine and the
display was set to 1280 × 768. The guest terminal ran `cat /etc/alpine-release`
and `uname -sr`. Future image and package versions can change the output.

Open Stoat in Konsole, select `alpine-desktop`, and press `l` for details.
Press **Escape** to return, then `n` to capture the creation form.
**Escape** closes the form without creating another VM.

```sh
env -u NO_COLOR TERM=xterm-256color COLORTERM=truecolor \
  QT_QPA_PLATFORM=xcb konsole -e stoat
```

On an X11/Xwayland window, find its ID with `xprop -root _NET_CLIENT_LIST`
and verify its title with `xprop -id <id> _NET_WM_NAME`. Record that window:

```sh
ffmpeg -f x11grab -draw_mouse 0 -framerate 15 -window_id "$WINDOW_ID" \
  -i "$DISPLAY" -c:v libx264 -crf 20 -pix_fmt yuv420p \
  -movflags +faststart capture.mp4
```

Set `WINDOW_ID` to the chosen window's numeric ID. Use a new output path
for each capture. Pure Wayland windows require a compositor-supported recorder.

## Stop the guests

```sh
stoat down alpine-desktop
stoat wait alpine-desktop --until stopped --timeout 30s
stoat down debian-dev
stoat wait debian-dev --until stopped --timeout 30s
```

Stopping Alpine discards its live desktop. See the
[live recipe restart limitation](../docs/troubleshooting.md#a-live-vm-lost-everything-after-a-reboot)
before reusing a live VM. Debian's disk and the VM definitions remain in
the capture data root.
