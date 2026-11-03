package service

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

var (
	ErrStartup = errors.New("service startup validation failed")
	ErrLocked  = errors.New("database is in use; stop the service before maintenance")
	ErrService = errors.New("service operation failed")
)

const (
	// ownerReadWriteMode is read and write for the owner and no access for
	// group or others. Database and lock files must have exactly this mode.
	//
	//	Digit  Who     Value                 Meaning
	//	6      owner   4 (read) + 2 (write)  read and write
	//	0      group   0                     no access
	//	0      others  0                     no access
	ownerReadWriteMode = os.FileMode(0o600)

	// groupOtherPermissions is every group and other permission bit.
	// The database directory is rejected when any of these bits is set.
	//
	//	Digit  Who     Value                                 Meaning
	//	0      owner   0                                     not tested
	//	7      group   4 (read) + 2 (write) + 1 (execute)    any access
	//	7      others  4 (read) + 2 (write) + 1 (execute)    any access
	groupOtherPermissions = os.FileMode(0o077)
)

// Paths resolves trusted parent directories before any database or network work.
// Existing final symlinks and shared database hardlinks are rejected.
func Paths(db, key string) (string, string, error) {
	resolve := func(p string) (string, error) {
		a, e := filepath.Abs(p)
		if e != nil {
			return "", ErrStartup
		}
		d, e := filepath.EvalSymlinks(filepath.Dir(a))
		if e != nil {
			return "", ErrStartup
		}
		return filepath.Join(d, filepath.Base(a)), nil
	}
	d, e := resolve(db)
	if e != nil {
		return "", "", e
	}
	k, e := resolve(key)
	if e != nil {
		return "", "", e
	}
	within := func(parent, p string) bool {
		r, e := filepath.Rel(parent, p)
		return e == nil && r != ".." && !filepath.IsAbs(r) && (r == "." || len(r) < 3 || r[:3] != "../")
	}
	if within(filepath.Dir(d), k) {
		return "", "", ErrStartup
	}
	// A key beneath a checkout is forbidden even when launched elsewhere.
	for p := filepath.Dir(k); ; p = filepath.Dir(p) {
		if _, err := os.Stat(filepath.Join(p, ".git")); err == nil {
			return "", "", ErrStartup
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	cwd, e := os.Getwd()
	if e != nil {
		return "", "", ErrStartup
	}
	for p := cwd; ; p = filepath.Dir(p) {
		if _, e := os.Stat(filepath.Join(p, ".git")); e == nil {
			real, e := filepath.EvalSymlinks(p)
			if e != nil || within(real, k) {
				return "", "", ErrStartup
			}
			break
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	info, e := os.Stat(filepath.Dir(d))
	if e != nil || !info.IsDir() || info.Mode().Perm()&groupOtherPermissions != 0 {
		return "", "", ErrStartup
	}
	if info, e := os.Lstat(d); e == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm() != ownerReadWriteMode {
			return "", "", ErrStartup
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Nlink != 1 {
			return "", "", ErrStartup
		}
	} else if !os.IsNotExist(e) {
		return "", "", ErrStartup
	}
	return d, k, nil
}

type Lock struct{ f *os.File }

// Acquire holds a persistent advisory lock file; it must never be unlinked while
// processes may be waiting on its inode. Every service and maintenance entry uses it.
func Acquire(db string) (*Lock, error) {
	fd, e := syscall.Open(db+".lock", syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, uint32(ownerReadWriteMode))
	if e != nil {
		return nil, ErrStartup
	}
	f := os.NewFile(uintptr(fd), "database maintenance lock")
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != ownerReadWriteMode {
		f.Close()
		return nil, ErrStartup
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Nlink != 1 {
		f.Close()
		return nil, ErrStartup
	}
	if syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		f.Close()
		return nil, ErrLocked
	}
	return &Lock{f}, nil
}
func (l *Lock) Close() error {
	if l.f.Close() != nil {
		return ErrService
	}
	return nil
}

// Opens a database file with the given path and creates it if create is true.
func databaseFile(path string, create bool) error {
	flags := syscall.O_RDWR | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_CLOEXEC
	if create {
		flags |= syscall.O_CREAT
	}

	descriptor, err := syscall.Open(path, flags, uint32(ownerReadWriteMode))
	if err != nil {
		return ErrStartup
	}

	file := os.NewFile(uintptr(descriptor), "database")
	defer file.Close()

	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != ownerReadWriteMode {
		return ErrStartup
	}
	return nil
}
