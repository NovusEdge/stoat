package apkovl

// postInstall runs on the installed disk after setup-alpine succeeds and
// before the installer powers off. It is sh, and it runs in the live
// installer environment, so every path it touches is under $target.
//
// setup-disk leaves nothing mounted for this to reuse, and the partition
// layout is whatever setup-disk chose, so mountTarget finds the root by
// looking for /etc/inittab rather than by assuming a partition number. The
// installer environment has no /etc/mtab worth reading either: the target
// was mounted and unmounted by a child process.
const postInstall = mountTarget + extlinuxTimeoutFix + workMountFix + issueFix + umountTarget

const mountTarget = `target=/mnt/target
mkdir -p "$target"
root=
for p in /dev/vda3 /dev/vda2 /dev/vda1; do
	[ -b "$p" ] || continue
	mount "$p" "$target" 2>/dev/null || continue
	if [ -f "$target/etc/inittab" ]; then root=$p; break; fi
	umount "$target"
done
if [ -z "$root" ]; then
	echo "stoat: no installed root found on /dev/vda; skipping the post-install fixes"
else
	# /boot is inside the root on a single-partition install and its own
	# partition otherwise. Either way extlinux.conf must be writable.
	[ -f "$target/boot/extlinux.conf" ] || mount /dev/vda1 "$target/boot" 2>/dev/null || true
`

const umountTarget = `	umount "$target/boot" 2>/dev/null || true
	umount "$target"
fi
`

// extlinuxTimeoutFix appends TOTALTIMEOUT to the installed extlinux.conf
// (issue #59). The installed config carries DEFAULT menu.c32, PROMPT 0,
// MENU HIDDEN and TIMEOUT 10, one second. syslinux cancels TIMEOUT and draws
// the hidden menu when input arrives during that second, and then waits with
// no countdown left. TOTALTIMEOUT fires whatever the user typed.
//
// update-extlinux.conf has no key for TOTALTIMEOUT, so the line is appended
// to the generated file. A kernel upgrade that reruns update-extlinux drops
// it again.
const extlinuxTimeoutFix = `	conf="$target/boot/extlinux.conf"
	if [ -f "$conf" ] && ! grep -q '^TOTALTIMEOUT ' "$conf"; then
		echo 'TOTALTIMEOUT 100' >> "$conf"
		echo "stoat: added TOTALTIMEOUT to extlinux.conf"
	fi
`

// workMountFix repairs the installed fstab's 9p lines (issue #60).
//
// setup-disk writes the target's fstab from the live system's mounts, and
// stoat's shares sit under /mnt, the same directory setup-disk mounts the
// target on, so the work share lands as "work /work 9p ... 0 2". busybox
// fsck then has no fsck.9p helper, and /work does not exist on the target,
// so the guest prints a failed mount on every boot.
//
// awk rewrites only lines whose type field is 9p; every other line prints
// unchanged, comments included.
const workMountFix = `	fstab="$target/etc/fstab"
	if [ -f "$fstab" ]; then
		awk '$3 == "9p" { $2 = "/mnt/" $1; $5 = "0"; $6 = "0" } { print }' "$fstab" > "$fstab.stoat" &&
			mv "$fstab.stoat" "$fstab"
		awk '$3 == "9p" { print $2 }' "$fstab" | while read -r d; do
			mkdir -p "$target$d"
		done
	fi
`

// issueFix restores the stock /etc/issue on the target (issue #61).
//
// setup-disk -m sys copies the live system's /etc, so the installed login
// screen still reads "Installing Alpine. Unattended. Do not log in." The
// guard on the banner text means a rerun never overwrites an issue the user
// has since edited.
//
// printf's format is single-quoted, so \r, \m and \l reach the file as the
// getty escapes \r, \m and \l rather than as control characters.
const issueFix = `	issue="$target/etc/issue"
	if [ -f "$issue" ] && grep -q 'Installing Alpine' "$issue"; then
		printf 'Welcome to Alpine Linux %s\nKernel \\r on an \\m (\\l)\n\n' \
			"$(cut -d. -f1,2 "$target/etc/alpine-release")" > "$issue"
		echo "stoat: restored the stock /etc/issue"
	fi
`
