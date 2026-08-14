//go:build windows

package processlock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

var ErrAlreadyLocked = errors.New("process lock already held")

type Lock struct {
	file *os.File
}

func TryLock(path string) (*Lock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	overlapped := new(windows.Overlapped)
	err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped)
	if err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrAlreadyLocked
		}
		return nil, err
	}
	return &Lock{file: file}, nil
}

func (lock *Lock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	overlapped := new(windows.Overlapped)
	err := windows.UnlockFileEx(windows.Handle(lock.file.Fd()), 0, 1, 0, overlapped)
	closeErr := lock.file.Close()
	lock.file = nil
	return errors.Join(err, closeErr)
}
