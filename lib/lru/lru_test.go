package lru

import (
	"testing"
)

func TestLruAppend(t *testing.T) {
	c := New[int, int](4)
	c.Set(1, 1)
	c.Set(2, 2)
	c.Set(3, 3)
	c.Set(4, 4)
	c.Set(5, 5)
	if c.Get(1) != 0 {
		t.FailNow()
	}
	if c.Get(5) != 5 {
		t.FailNow()
	}
}

func TestLruChange(t *testing.T) {
	c := New[int, int](4)
	c.Set(1, 1)
	c.Set(2, 2)
	c.Set(3, 3)
	c.Set(4, 4)
	c.Set(1, 5)
	if c.Get(1) != 5 {
		t.FailNow()
	}
}

func TestLruDel(t *testing.T) {
	c := New[int, int](4)
	c.Set(1, 1)
	c.Set(2, 2)
	c.Set(3, 3)
	c.Set(4, 4)
	c.Del(2)
	if c.List.Size != c.Len() || c.Len() != 3 {
		t.FailNow()
	}
	if c.Get(2) != 0 {
		t.FailNow()
	}
}

func TestLruSize(t *testing.T) {
	c := New[int, int](4)
	if c.List.Size != c.Len() || c.Len() != 0 {
		t.FailNow()
	}
	c.Set(1, 1)
	if c.List.Size != c.Len() || c.Len() != 1 {
		t.FailNow()
	}
	c.Set(2, 2)
	if c.List.Size != c.Len() || c.Len() != 2 {
		t.FailNow()
	}
	c.Set(3, 3)
	if c.List.Size != c.Len() || c.Len() != 3 {
		t.FailNow()
	}
	c.Set(4, 4)
	if c.List.Size != c.Len() || c.Len() != 4 {
		t.FailNow()
	}
	c.Set(5, 5)
	if c.List.Size != c.Len() || c.Len() != 4 {
		t.FailNow()
	}
}
