// Package priority implements a priority mutex.
package priority

import (
	"sync"
)

// Priority implement a lock with priorities.
type Priority struct {
	l []sync.Mutex
}

// Call the function f with priority.
func (p *Priority) Pri(n int, f func() error) error {
	for i := n; i >= 0; i-- {
		p.l[i].Lock()
	}
	err := f()
	for i := 0; i <= n; i++ {
		p.l[i].Unlock()
	}
	return err
}

// NewPriority returns a new Priority with n priority levels.
func NewPriority(n int) *Priority {
	return &Priority{
		l: make([]sync.Mutex, n),
	}
}
