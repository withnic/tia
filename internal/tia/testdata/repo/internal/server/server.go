package server

import (
	. "strings"
	_ "embed"

	"example.com/shop/internal/db"
	"example.com/shop/internal/service"
	"github.com/jackc/pgx/v5"
	yaml "gopkg.in/yaml.v3"
	"github.com/mattn/go-sqlite3"
)

type Server struct{ svc service.UserService }

func NewServer() *Server { return &Server{svc: service.NewUserService(nil)} }

func InitializeRoutes(s *Server) {
	_ = ToUpper("x")
	_ = database.DefaultTimeout
	_ = pgx.Connect
	_ = yaml.Marshal
	_ = sqlite3.Version
}
