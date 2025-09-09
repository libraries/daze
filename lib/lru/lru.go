// Package lru implements an LRU cache.
package lru

import (
	"sync"
)

// Elem is an element of a linked list.
type Elem[K comparable, V any] struct {
	Next, Prev *Elem[K, V]
	K          K
	V          V
}

// List represents a doubly linked list.
type List[K comparable, V any] struct {
	Root *Elem[K, V]
	Size int
}

// Init initializes or clears list l.
func (l *List[K, V]) Init() *List[K, V] {
	root := Elem[K, V]{}
	root.Prev = &root
	root.Next = &root
	l.Root = &root
	l.Size = 0
	return l
}

// Insert inserts e after root, increments l's size, and returns e.
func (l *List[K, V]) Insert(e *Elem[K, V]) *Elem[K, V] {
	e.Prev = l.Root
	e.Next = l.Root.Next
	e.Prev.Next = e
	e.Next.Prev = e
	l.Size++
	return e
}

// Remove removes e from its list, decrements l's size.
func (l *List[K, V]) Remove(e *Elem[K, V]) {
	e.Prev.Next = e.Next
	e.Next.Prev = e.Prev
	e.Prev = nil // Avoid memory leaks
	e.Next = nil // Avoid memory leaks
	l.Size--
}

// Update e to next to root.
func (l *List[K, V]) Update(e *Elem[K, V]) {
	if l.Root.Next == e {
		return
	}
	e.Prev.Next = e.Next
	e.Next.Prev = e.Prev
	e.Prev = l.Root
	e.Next = l.Root.Next
	e.Prev.Next = e
	e.Next.Prev = e
}

// Lru cache. It is safe for concurrent access.
type Lru[K comparable, V any] struct {
	// Drop is called automatically when an elem is deleted.
	Drop func(k K, v V)
	// Size is the maximum number of cache entries before an item is evicted. Zero means no limit.
	Size int
	List *List[K, V]
	C    map[K]*Elem[K, V]
	M    *sync.Mutex
}

// Del removes the provided key from the cache.
func (l *Lru[K, V]) Del(k K) {
	l.M.Lock()
	defer l.M.Unlock()
	if e, ok := l.C[k]; ok {
		l.Drop(k, e.V)
		delete(l.C, k)
		l.List.Remove(e)
	}
}

// Get looks up a key's value from the cache.
func (l *Lru[K, V]) GetExists(k K) (v V, ok bool) {
	l.M.Lock()
	defer l.M.Unlock()
	var e *Elem[K, V]
	e, ok = l.C[k]
	if ok {
		l.List.Update(e)
		v = e.V
	}
	return
}

// Get looks up a key's value from the cache.
func (l *Lru[K, V]) Get(k K) (v V) {
	v, _ = l.GetExists(k)
	return
}

// Has returns true if a key exists.
func (l *Lru[K, V]) Has(k K) bool {
	l.M.Lock()
	defer l.M.Unlock()
	_, b := l.C[k]
	return b
}

// Len returns the number of items in the cache.
func (l *Lru[K, V]) Len() int {
	l.M.Lock()
	defer l.M.Unlock()
	return l.List.Size
}

// Set adds a value to the cache.
func (l *Lru[K, V]) Set(k K, v V) {
	l.M.Lock()
	defer l.M.Unlock()
	if e, ok := l.C[k]; ok {
		l.List.Update(e)
		e.K = k
		e.V = v
		return
	}
	if l.List.Size == l.Size {
		l.Drop(l.List.Root.Prev.K, l.List.Root.Prev.V)
		delete(l.C, l.List.Root.Prev.K)
		l.List.Remove(l.List.Root.Prev)
	}
	l.C[k] = l.List.Insert(&Elem[K, V]{K: k, V: v})
}

// New returns a new LRU cache. If size is zero, the cache has no limit.
func New[K comparable, V any](size int) *Lru[K, V] {
	return &Lru[K, V]{
		Drop: func(k K, v V) {},
		Size: size,
		List: new(List[K, V]).Init(),
		C:    map[K]*Elem[K, V]{},
		M:    &sync.Mutex{},
	}
}
