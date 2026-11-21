package repo

import (
	"context"
	"testing"
)

// A unit test (no integration tag / suffix): never selected.
func TestRepoUnit(t *testing.T) { _ = NewUserRepo().Create(context.Background(), "u") }
