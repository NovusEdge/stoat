package config

import (
	"os"
	"path/filepath"
	"syscall"
)

// lockName is the lock file in the data root. A dotfile so it never appears in
// a VM listing: List and ListBroken enumerate DIRECTORIES under the root, and
// this is a plain file, so neither sees it either way, but the dot makes that
// obvious to anyone reading the directory.
const lockName = ".lock"

// Lock takes an exclusive advisory lock on the data root and returns the
// function that releases it. It BLOCKS until the lock is available.
//
// Allocating a resource and committing it are two separate steps, with a
// gap in between. Everything stoat allocates gets claimed later, by writing
// it into a vm.toml. FreePort reads every VM's port, picks a free one, and
// returns it; the caller writes it to disk sometime after that. Two callers
// interleaved in that gap can both see the same free port and both take it.
// The result is two VMs fighting over one host socket, which surfaces much
// later as a bare bind failure naming neither VM. The name-already-exists
// check in Create and Clone has the same shape.
//
// IT IS A FILE LOCK, NOT A MUTEX, and that is the point: stoat is a CLI.
// The realistic collision is two `stoat create` invocations, or an MCP
// server racing a human's terminal: separate PROCESSES. An in-process
// mutex would serialise goroutines that were never the problem, and do
// nothing about the collision that is real. flock also releases
// automatically when the holder process exits, so a process killed
// mid-create cannot leave the data root locked forever, the way a lock
// file holding a pid can.
//
// The lock is advisory: it binds only code that calls Lock. Nothing stops
// a user hand-editing vm.toml directly; that is out of scope.
//
// # THESE LOCKS DO NOT NEST
//
// flock associates a lock with an OPEN FILE DESCRIPTION, not a process.
// Taking the same lock twice from one process, through two separate opens,
// BLOCKS FOREVER against itself. It is not a reentrant mutex.
//
// This bit Clone: it holds Lock, then calls keys.Ensure. When
// keys.generate also took Lock, any data root without a key yet
// deadlocked. That is why keys has its own lock file (LockKeys) instead of
// sharing this one. Before calling Lock, check that nothing downstream of
// you takes it too. If two operations genuinely need to serialise against
// each other, they must share ONE lock taken ONCE, never take it at two
// levels.
func Lock() (func(), error) { return lockFile(lockName) }

// keysLockName is a SEPARATE lock file, so key generation can serialise
// against other processes without deadlocking against a data-root lock its
// caller may already hold; see the nesting note on Lock. The two protect
// different things: this one covers the shared keypair, Lock covers VM
// allocation, and no operation needs both to be one atomic unit.
const keysLockName = ".keys.lock"

// LockKeys serialises keypair generation across processes. See Lock for the
// mechanics and the nesting rule.
func LockKeys() (func(), error) { return lockFile(keysLockName) }

func lockFile(name string) (func(), error) {
	if err := EnsureRoot(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(Root(), name), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		// Unlock explicitly rather than relying on Close: the ordering is
		// then visible to a reader, and Close alone would still be correct
		// only because this fd is not shared.
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
