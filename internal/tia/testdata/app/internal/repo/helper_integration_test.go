package repo

import "context"

func seedUsers(r *UserRepo) { _ = r.Create(context.Background(), "seed") }
