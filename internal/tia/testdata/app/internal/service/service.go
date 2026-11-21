package service

import (
	"context"

	"example.com/app/internal/repo"
)

type UserService interface {
	Create(ctx context.Context, name string) error
}

type userService struct{ repo *repo.UserRepo }

func NewUserService(r *repo.UserRepo) UserService { return &userService{repo: r} }

func (s *userService) Create(ctx context.Context, name string) error {
	return s.repo.Create(ctx, name)
}
