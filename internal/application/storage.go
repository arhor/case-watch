package application

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

var (
	errStartup = errors.New("service startup validation failed")
	errLocked  = errors.New("database is in use; stop the service before maintenance")
	errService = errors.New("service operation failed")
)

const (
	// Restrict database and lock files to the service account.
	ownerReadWriteMode    = os.FileMode(0o600)
	groupOtherPermissions = os.FileMode(0o077)
)

// databasePath resolves the trusted parent directory before database or network work.
// Existing final symlinks and shared database hardlinks are rejected.
func databasePath(db string) (string, error) {
	resolve := func(p string) (string, error) {
		a, e := filepath.Abs(p)
		if e != nil {
			return "", errStartup
		}
		d, e := filepath.EvalSymlinks(filepath.Dir(a))
		if e != nil {
			return "", errStartup
		}
		return filepath.Join(d, filepath.Base(a)), nil
	}
	d, e := resolve(db)
	if e != nil {
		return "", e
	}
	info, e := os.Stat(filepath.Dir(d))
	if e != nil || !info.IsDir() || info.Mode().Perm()&groupOtherPermissions != 0 {
		return "", errStartup
	}
	if info, e := os.Lstat(d); e == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm() != ownerReadWriteMode {
			return "", errStartup
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Nlink != 1 {
			return "", errStartup
		}
	} else if !os.IsNotExist(e) {
		return "", errStartup
	}
	return d, nil
}

type databaseLock struct{ f *os.File }

// acquire holds a persistent advisory lock file; it must never be unlinked while
// processes may be waiting on its inode. Every service and maintenance entry uses it.
func acquire(db string) (*databaseLock, error) {
	fd, e := syscall.Open(db+".lock", syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, uint32(ownerReadWriteMode))
	if e != nil {
		return nil, errStartup
	}
	f := os.NewFile(uintptr(fd), "database maintenance lock")
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != ownerReadWriteMode {
		f.Close()
		return nil, errStartup
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Nlink != 1 {
		f.Close()
		return nil, errStartup
	}
	if syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		f.Close()
		return nil, errLocked
	}
	return &databaseLock{f}, nil
}
func (l *databaseLock) close() error {
	if l.f.Close() != nil {
		return errService
	}
	return nil
}

// databaseFile creates or validates the protected SQLite file.
func databaseFile(path string) error {
	flags := syscall.O_CREAT | syscall.O_RDWR | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_CLOEXEC

	descriptor, err := syscall.Open(path, flags, uint32(ownerReadWriteMode))
	if err != nil {
		return errStartup
	}

	file := os.NewFile(uintptr(descriptor), "database")
	defer file.Close()

	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != ownerReadWriteMode {
		return errStartup
	}
	return nil
}
