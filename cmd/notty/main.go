package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"

	"github.com/flanfranchi1/notty/internal/auth"
	"github.com/flanfranchi1/notty/internal/database"
	"github.com/flanfranchi1/notty/internal/handlers"
	"github.com/flanfranchi1/notty/internal/i18n"
)

type Config struct {
	Port        string
	StoragePath string
	SessionName string
}

func loadConfig() Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "0.0.0.0:8080"
	}
	storagePath := os.Getenv("STORAGE_PATH")
	if storagePath == "" {
		storagePath = "./storage"
	}
	sessionName := os.Getenv("SESSION_NAME")
	if sessionName == "" {
		sessionName = "notty_session"
	}
	return Config{
		Port:        port,
		StoragePath: storagePath,
		SessionName: sessionName,
	}
}

func main() {
	cfg := loadConfig()

	mgr := database.NewManager(cfg.StoragePath)
	systemDB, err := mgr.InitSystemDB()
	if err != nil {
		log.Fatalf("failed to init system db: %v", err)
	}
	defer systemDB.Close()

	sessions := auth.NewSessionStore()

	bundle, err := i18n.LoadEmbedded()
	if err != nil {
		log.Fatalf("failed to load i18n bundle: %v", err)
	}

	seeded, skipped, err := mgr.BackfillTutorialShowcase(systemDB, bundle.Translations("en"))
	if err != nil {
		log.Printf("tutorial backfill failed: %v", err)
	} else {
		log.Printf("tutorial backfill completed: seeded=%d skipped=%d", seeded, skipped)
	}

	// dict builds a map[string]interface{} from alternating key/value pairs.
	// This is required so that templates can forward both note data AND the
	// outer translation map ($.T) when calling sub-templates from inside a
	// {{range}} block — range changes the context to the range element and
	// without dict there is no clean way to pass the T map alongside it.
	funcMap := template.FuncMap{
		"dict": func(pairs ...interface{}) (map[string]interface{}, error) {
			if len(pairs)%2 != 0 {
				return nil, fmt.Errorf("dict requires an even number of arguments, got %d", len(pairs))
			}
			m := make(map[string]interface{}, len(pairs)/2)
			for i := 0; i < len(pairs); i += 2 {
				key, ok := pairs[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict key at position %d must be a string", i)
				}
				m[key] = pairs[i+1]
			}
			return m, nil
		},
	}
	templates := template.Must(template.New("pages").Funcs(funcMap).ParseGlob("./web/templates/*.gohtml"))

	server := &handlers.Server{
		DBManager:         mgr,
		SessionStore:      sessions,
		SystemDB:          systemDB,
		Templates:         templates,
		SessionCookieName: cfg.SessionName,
		Bundle:            bundle,
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		server.RenderTemplate(w, r, "home.gohtml", nil)
	})
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./web/static"))))
	mux.HandleFunc("/signup", server.SignupHandler)
	mux.HandleFunc("/login", server.LoginHandler)
	mux.HandleFunc("/notes", server.NotesHandler)
	mux.HandleFunc("/notes/create", server.CreateNoteHandler)
	mux.HandleFunc("/inbox", server.InboxHandler)
	mux.HandleFunc("/notebooks/create", server.CreateNotebookHandler)
	mux.HandleFunc("/notebooks/", server.NotebookViewHandler)
	mux.HandleFunc("/notes/view", server.ViewNoteHandler)
	mux.HandleFunc("/notes/view/edit", server.ViewNoteEditHandler)
	mux.HandleFunc("/notes/view/update", server.ViewNoteUpdateHandler)
	mux.HandleFunc("/notes/", server.NoteActionHandler)
	mux.HandleFunc("/search", server.SearchHandler)
	mux.HandleFunc("/tags/", server.TagsHandler)
	mux.HandleFunc("/logout", server.LogoutHandler)
	mux.HandleFunc("/forgot-password", server.ForgotPasswordHandler)

	log.Printf("starting server on %s", cfg.Port)
	if err := http.ListenAndServe(cfg.Port, handlers.I18nMiddleware(mux)); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
