package service_test

import (
	"context"
	"testing"

	"example.com/app/internal/repo"
	"example.com/app/internal/service"
)

func TestServiceCreate(t *testing.T) {
	if err := service.NewUserService(repo.NewUserRepo()).Create(context.Background(), "s"); err != nil {
		t.Fatal(err)
	}
}
