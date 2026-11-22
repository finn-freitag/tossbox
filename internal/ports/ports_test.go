package ports

import "testing"

func TestAllocate(t *testing.T) {
	a := Allocator{Start: 100, End: 104}
	used := map[int]bool{101: true}
	got, err := a.Allocate(used, 3)
	if err != nil || len(got) != 3 || got[0] != 100 || got[1] != 102 || got[2] != 103 {
		t.Fatalf("got %v %v", got, err)
	}
	if _, err := a.Allocate(used, 2); err != ErrExhausted {
		t.Fatalf("want exhausted, got %v", err)
	}
	if used[104] {
		t.Fatal("failed allocation must roll back")
	}
}
