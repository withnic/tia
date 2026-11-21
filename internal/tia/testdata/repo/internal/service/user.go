package service

import (
	"context"

	dbx "example.com/shop/internal/db"
	"example.com/shop/internal/repo"
)

// UserService manages users.
type UserService interface {
	Create(ctx context.Context, name string) error
}

type userServiceImpl struct {
	repo *repo.UserRepo
}

// NewUserService builds a UserService.
func NewUserService(r *repo.UserRepo) UserService {
	return &userServiceImpl{repo: r}
}

func (s *userServiceImpl) Create(ctx context.Context, name string) error {
	return s.repo.Create(ctx, name)
}

type Set[T comparable] struct{ m map[T]struct{} }

func (s *Set[T]) Add(v T) { s.m[v] = struct{}{} }

func (s Set[T]) Len() int { return len(s.m) }

const (
	StatusActive = iota
	StatusInactive
)

var MaxRetry, Timeout = 3, dbx.DefaultTimeout

type ID string

type Alias = ID

func init() {}

func init() {}
