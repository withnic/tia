package handler

import "example.com/app/internal/order"

type OrderHandler struct{ svc *order.OrderService }

func NewOrderHandler(svc *order.OrderService) *OrderHandler { return &OrderHandler{svc: svc} }

func (h *OrderHandler) HandleOrder(item string) error { return h.svc.Create(item) }
