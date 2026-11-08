package application

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

var (
	errStartup = errors.New("service startup validation failed")
	errLocked  = errors.New("another case-watch instance is already running")
	errService = errors.New("service operation failed")
)

// databasePath requires a private directory for the stored portal credentials.
func databasePath(db string) (string, error) {
	path, err := filepath.Abs(db)
	if err != nil {
		return "", errStartup
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil || !dir.IsDir() || dir.Mode().Perm()&0o077 != 0 {
		return "", errStartup
	}
	return path, nil
}

// acquireInstanceLock prevents two bot processes from running against the same
// database. Keep the lock file in place so flock always uses the same inode.
func acquireInstanceLock(db string) (*os.File, error) {
	file, err := os.OpenFile(db+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, errStartup
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errLocked
		}
		return nil, errStartup
	}
	return file, nil
}

// databaseFile creates a private SQLite file or restricts an existing one.
func databaseFile(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return errStartup
	}
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return errStartup
	}
	return nil
}
