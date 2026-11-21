package repo

import (
	"context"
	"testing"
)

func TestRepoCreate(t *testing.T) {
	if err := NewUserRepo().Create(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
}

func TestRepoList(t *testing.T) {
	r := NewUserRepo()
	seedUsers(r)
	if len(r.List()) != 1 {
		t.Fatal("want 1 row")
	}
}
