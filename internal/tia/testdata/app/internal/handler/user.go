package handler

import (
	"context"

	"example.com/app/internal/service"
)

type UserHandler struct{ svc service.UserService }

func NewUserHandler(svc service.UserService) *UserHandler { return &UserHandler{svc: svc} }

func (h *UserHandler) Handle(name string) error { return h.svc.Create(context.Background(), name) }
