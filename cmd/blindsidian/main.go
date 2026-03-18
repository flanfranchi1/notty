package main

import (
	"html/template"
	"log"
	"net/http"

	"github.com/felan/blindsidian/internal/auth"
	"github.com/felan/blindsidian/internal/database"
	"github.com/felan/blindsidian/internal/handlers"
)

func main() {
	mgr := database.NewManager("./storage")
	systemDB, err := mgr.InitSystemDB()
	if err != nil {
		log.Fatalf("failed to init system db: %v", err)
	}
	defer systemDB.Close()

	sessions := auth.NewSessionStore()

	templates := template.Must(template.New("pages").ParseGlob("./web/templates/*.gohtml"))

	server := &handlers.Server{
		DBManager:    mgr,
		SessionStore: sessions,
		SystemDB:     systemDB,
		Templates:    templates,
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		server.RenderTemplate(w, "home.gohtml", nil)
	})
	http.HandleFunc("/signup", server.SignupHandler)
	http.HandleFunc("/login", server.LoginHandler)
	http.HandleFunc("/notes", server.NotesHandler)
	http.HandleFunc("/notes/create", server.CreateNoteHandler)
	http.HandleFunc("/notebooks/create", server.CreateNotebookHandler)
	http.HandleFunc("/notebooks/", server.NotebookViewHandler)
	http.HandleFunc("/notes/view", server.ViewNoteHandler)
	http.HandleFunc("/notes/view/edit", server.ViewNoteEditHandler)
	http.HandleFunc("/notes/view/update", server.ViewNoteUpdateHandler)
	http.HandleFunc("/notes/", server.NoteActionHandler)
	http.HandleFunc("/search", server.SearchHandler)
	http.HandleFunc("/logout", server.LogoutHandler)

	addr := ":8080"
	log.Printf("starting server on %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
