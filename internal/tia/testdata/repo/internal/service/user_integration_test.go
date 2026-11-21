package service_test

import (
	"context"
	"testing"

	"example.com/shop/internal/service"
)

func helperCreate(t *testing.T) error {
	return service.NewUserService(nil).Create(context.Background(), "x")
}

func TestCreateUser(t *testing.T) { _ = helperCreate(t) }

func Testlowercase(t *testing.T) {}

func TestMain(m *testing.M) { m.Run() }

func Test(t *testing.T) {}
