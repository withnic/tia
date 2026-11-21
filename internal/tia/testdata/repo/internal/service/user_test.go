package service

import "testing"

func newSet() *Set[int] { return &Set[int]{m: map[int]struct{}{}} }

func TestSet(t *testing.T) {
	s := newSet()
	s.Add(1)
}
