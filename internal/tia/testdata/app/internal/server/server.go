// Package server wires every handler: a typical hub.
package server

import "example.com/app/internal/di"

type Server struct{ routes map[string]func(string) error }

func NewServer() *Server {
	users := di.InitializeUserHandler()
	orders := di.InitializeOrderHandler()
	return &Server{routes: map[string]func(string) error{
		"/users":  users.Handle,
		"/orders": orders.HandleOrder,
	}}
}

func (s *Server) Serve(path, arg string) error { return s.routes[path](arg) }
