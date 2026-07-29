package pool

import "sync"

type Lease interface {
	KeyID() int64
	Release()
}

type lease struct {
	keyID   int64
	release func()
	once    sync.Once
}

func (l *lease) KeyID() int64 {
	return l.keyID
}

func (l *lease) Release() {
	l.once.Do(l.release)
}
