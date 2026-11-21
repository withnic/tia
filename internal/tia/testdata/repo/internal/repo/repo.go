package repo

import "context"

type UserRepo struct{}

func (r *UserRepo) Create(ctx context.Context, name string) error { return nil }
