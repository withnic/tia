package e2e

import (
	"testing"

	"example.com/app/internal/di"
)

func TestUsersAPI(t *testing.T) {
	if err := di.InitializeUserHandler().Handle("e2e"); err != nil {
		t.Fatal(err)
	}
}
