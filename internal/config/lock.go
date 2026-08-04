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
// This exists because allocating a resource and committing it are two steps
// with a gap in between, and everything stoat allocates is claimed by writing
// it into a vm.toml LATER: FreePort reads every VM's port, picks one that is
// free, and returns it; the caller writes it to disk some time after that.
// Two callers interleaved in that gap both see the same free port and both
// take it, producing two VMs that fight over one host socket, which surfaces
// much later as a bare bind failure from qemu naming neither VM. The same
// shape applies to the name-already-exists check in Create and Clone.
//
// IT IS A FILE LOCK, NOT A MUTEX, and that is the whole point: stoat is a CLI.
// The realistic collision is two `stoat create` invocations, or an MCP server
// and a human's terminal, which are separate PROCESSES: an in-process mutex
// would serialise goroutines that were never the problem while doing nothing
// at all about the case that is. flock is also released automatically by the
// kernel when the holder exits, so a process killed mid-create cannot leave
// the data root permanently locked, which a lock file containing a pid can.
//
// Advisory, so it binds only code that calls Lock. Nothing stops a user
// hand-editing vm.toml; that is out of scope and always was.
//
// # THESE LOCKS DO NOT NEST
//
// flock associates a lock with an OPEN FILE DESCRIPTION, not with a process,
// so taking the same lock twice from one process (via two separate opens)
// BLOCKS FOREVER against itself. It is not a reentrant mutex.
//
// This is not hypothetical: Clone holds Lock and then calls keys.Ensure, and
// when keys.generate also took Lock the whole test suite deadlocked on any
// data root without a key yet. That is why keys has its OWN lock file
// (LockKeys) rather than sharing this one. Before calling Lock, check that
// nothing downstream of you takes it; if two things genuinely need
// serialising against each other, they must share ONE lock taken ONCE, not
// take it at two levels.
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
