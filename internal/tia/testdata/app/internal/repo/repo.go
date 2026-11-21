package repo

import "context"

type UserRepo struct{ rows []string }

func NewUserRepo() *UserRepo { return &UserRepo{} }

func (r *UserRepo) Create(_ context.Context, name string) error {
	r.rows = append(r.rows, name)
	return nil
}

func (r *UserRepo) List() []string { return r.rows }
