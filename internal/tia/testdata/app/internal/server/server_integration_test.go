package server

import "testing"

func TestServerAPI(t *testing.T) {
	if err := NewServer().Serve("/users", "x"); err != nil {
		t.Fatal(err)
	}
}
