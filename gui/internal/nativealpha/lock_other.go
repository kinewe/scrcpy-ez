//go:build !windows

package nativealpha

import "sync"

var locks sync.Map

func Lock(path string) (func(), error) {
	v, _ := locks.LoadOrStore(path, &sync.Mutex{})
	m := v.(*sync.Mutex)
	m.Lock()
	return m.Unlock, nil
}
