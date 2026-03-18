package handlers

import (
	"database/sql"
	"html/template"

	"github.com/felan/blindsidian/internal/auth"
	"github.com/felan/blindsidian/internal/database"
)

type Server struct {
	DBManager    *database.DatabaseManager
	SessionStore *auth.SessionStore
	SystemDB     *sql.DB
	Templates    *template.Template
}
