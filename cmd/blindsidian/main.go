package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"html"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/felan/blindsidian/internal/database"
	"github.com/felan/blindsidian/internal/markup"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookieName = "blindsidian_session"
	sessionDuration   = 24 * time.Hour
)

var templates = template.Must(template.New("pages").ParseGlob("./web/templates/*.gohtml"))

type SessionStore struct {
	store map[string]string
}

func NewSessionStore() *SessionStore {
	return &SessionStore{store: map[string]string{}}
}

func (s *SessionStore) CreateSession(userID string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	s.store[token] = userID
	return token, nil
}

func (s *SessionStore) GetUserID(token string) (string, bool) {
	userID, ok := s.store[token]
	return userID, ok
}

func (s *SessionStore) DeleteSession(token string) {
	delete(s.store, token)
}

func renderTemplate(w http.ResponseWriter, name string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "unable to render page", http.StatusInternalServerError)
	}
}

func signupHandler(mgr *database.DatabaseManager, systemDB *sql.DB, sessions *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		errMsg := ""
		if r.Method == http.MethodPost {
			email := r.FormValue("email")
			password := r.FormValue("password")
			if email == "" || password == "" {
				errMsg = "Email and password are required."
			} else {
				existingUser, err := mgr.GetUserByEmail(systemDB, email)
				if err != nil {
					http.Error(w, "internal error", http.StatusInternalServerError)
					return
				}
				if existingUser != nil {
					errMsg = "Email is already registered."
				} else {
					uid := uuid.NewString()
					hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
					if err != nil {
						http.Error(w, "unable to hash password", http.StatusInternalServerError)
						return
					}

					if err := mgr.CreateSystemUser(systemDB, database.User{ID: uid, Email: email, PasswordHash: string(hash)}); err != nil {
						http.Error(w, "unable to create user", http.StatusInternalServerError)
						return
					}

					if _, err := mgr.CreateUserDB(uid); err != nil {
						http.Error(w, "unable to initialize user storage", http.StatusInternalServerError)
						return
					}

					token, err := sessions.CreateSession(uid)
					if err != nil {
						http.Error(w, "unable to create session", http.StatusInternalServerError)
						return
					}

					http.SetCookie(w, &http.Cookie{
						Name:     sessionCookieName,
						Value:    token,
						Path:     "/",
						HttpOnly: true,
						SameSite: http.SameSiteLaxMode,
						Secure:   false,
						Expires:  time.Now().Add(sessionDuration),
					})
					http.Redirect(w, r, "/notes", http.StatusSeeOther)
					return
				}
			}
		}

		renderTemplate(w, "signup.gohtml", map[string]string{"Error": errMsg})
	}
}

func loginHandler(mgr *database.DatabaseManager, systemDB *sql.DB, sessions *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		errMsg := ""
		if r.Method == http.MethodPost {
			email := r.FormValue("email")
			password := r.FormValue("password")
			if email == "" || password == "" {
				errMsg = "Email and password are required."
			} else {
				user, err := mgr.GetUserByEmail(systemDB, email)
				if err != nil {
					http.Error(w, "internal error", http.StatusInternalServerError)
					return
				}
				if user == nil {
					errMsg = "Invalid email or password."
				} else if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
					errMsg = "Invalid email or password."
				} else {
					token, err := sessions.CreateSession(user.ID)
					if err != nil {
						http.Error(w, "unable to create session", http.StatusInternalServerError)
						return
					}
					http.SetCookie(w, &http.Cookie{
						Name:     sessionCookieName,
						Value:    token,
						Path:     "/",
						HttpOnly: true,
						SameSite: http.SameSiteLaxMode,
						Secure:   false,
						Expires:  time.Now().Add(sessionDuration),
					})
					http.Redirect(w, r, "/notes", http.StatusSeeOther)
					return
				}
			}
		}
		renderTemplate(w, "login.gohtml", map[string]string{"Error": errMsg})
	}
}

func currentUserID(r *http.Request, sessions *SessionStore) (string, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", false
	}
	userID, ok := sessions.GetUserID(cookie.Value)
	return userID, ok
}

func notesHandler(mgr *database.DatabaseManager, sessions *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r, sessions)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		db, err := mgr.OpenUserDB(userID)
		if err != nil {
			http.Error(w, "unable to open user database", http.StatusInternalServerError)
			return
		}
		defer db.Close()

		notes, err := mgr.ListNotes(db)
		if err != nil {
			http.Error(w, "unable to load notes", http.StatusInternalServerError)
			return
		}

		type RenderNote struct {
			ID           string
			Title        string
			Content      string
			UpdatedAt    string
			RenderedHTML template.HTML
		}

		noteResolver := func(title string) (string, bool, error) {
			n, err := mgr.GetNoteByTitle(db, title)
			if err != nil {
				return "", false, err
			}
			if n == nil {
				return "", false, nil
			}
			return n.ID, true, nil
		}

		rendered := []RenderNote{}
		for _, note := range notes {
			htmlContent, err := markup.RenderMarkdownWithWikiLinks(note.Content, noteResolver)
			if err != nil {
				http.Error(w, "unable to render markdown", http.StatusInternalServerError)
				return
			}
			rendered = append(rendered, RenderNote{
				ID:           note.ID,
				Title:        note.Title,
				Content:      note.Content,
				UpdatedAt:    note.UpdatedAt,
				RenderedHTML: template.HTML(htmlContent),
			})
		}

		createTitle := r.URL.Query().Get("create")
		message := r.URL.Query().Get("msg")
		notebooks, err := mgr.ListNotebooks(db)
		if err != nil {
			notebooks = []database.Notebook{}
		}
		renderTemplate(w, "notes.gohtml", map[string]interface{}{"Notes": rendered, "Message": message, "CreateTitle": createTitle, "Notebooks": notebooks})
	}
}

func createNoteHandler(mgr *database.DatabaseManager, sessions *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r, sessions)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		if r.Method == http.MethodGet {
			createTitle := r.URL.Query().Get("title")
			renderTemplate(w, "notes.gohtml", map[string]interface{}{"CreateTitle": createTitle})
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		title := r.FormValue("title")
		content := r.FormValue("content")
		if title == "" || content == "" {
			db, err := mgr.OpenUserDB(userID)
			if err != nil {
				http.Error(w, "unable to open user database", http.StatusInternalServerError)
				return
			}
			defer db.Close()

			notes, err := mgr.ListNotes(db)
			if err != nil {
				http.Error(w, "unable to load notes", http.StatusInternalServerError)
				return
			}

			type RenderNote struct {
				ID           string
				Title        string
				Content      string
				UpdatedAt    string
				RenderedHTML template.HTML
			}

			noteResolver := func(title string) (string, bool, error) {
				n, err := mgr.GetNoteByTitle(db, title)
				if err != nil {
					return "", false, err
				}
				if n == nil {
					return "", false, nil
				}
				return n.ID, true, nil
			}

			rendered := []RenderNote{}
			for _, note := range notes {
				htmlContent, err := markup.RenderMarkdownWithWikiLinks(note.Content, noteResolver)
				if err != nil {
					http.Error(w, "unable to render markdown", http.StatusInternalServerError)
					return
				}
				rendered = append(rendered, RenderNote{
					ID:           note.ID,
					Title:        note.Title,
					Content:      note.Content,
					UpdatedAt:    note.UpdatedAt,
					RenderedHTML: template.HTML(htmlContent),
				})
			}

			data := map[string]interface{}{
				"Notes":       rendered,
				"CreateTitle": title,
				"Content":     content,
			}
			if title == "" {
				data["TitleError"] = "Title is required"
			}
			if content == "" {
				data["ContentError"] = "Content is required"
			}
			renderTemplate(w, "notes.gohtml", data)
			return
		}

		db, err := mgr.OpenUserDB(userID)
		if err != nil {
			http.Error(w, "unable to open user database", http.StatusInternalServerError)
			return
		}
		defer db.Close()

		existing, err := mgr.GetNoteByTitle(db, title)
		if err != nil {
			http.Error(w, "unable to check existing note", http.StatusInternalServerError)
			return
		}
		if existing != nil {
			http.Error(w, "note title already exists", http.StatusConflict)
			return
		}

		note := database.Note{ID: uuid.NewString(), Title: title, Content: content, NotebookID: r.FormValue("notebook_id")}
		if err := mgr.CreateNote(db, note); err != nil {
			http.Error(w, "unable to create note", http.StatusInternalServerError)
			return
		}

		titles := markup.ParseWikiLinks(note.Content)
		targetIDs := []string{}
		for _, t := range titles {
			target, err := mgr.GetNoteByTitle(db, t)
			if err != nil {
				continue
			}
			if target != nil {
				targetIDs = append(targetIDs, target.ID)
			}
		}
		mgr.InsertNoteLinks(db, note.ID, targetIDs)

		tags := markup.ParseTags(note.Content)
		mgr.InsertNoteTags(db, note.ID, tags)

		http.Redirect(w, r, "/notes?msg=Note+saved", http.StatusSeeOther)
	}
}

func noteActionHandler(mgr *database.DatabaseManager, sessions *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r, sessions)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		db, err := mgr.OpenUserDB(userID)
		if err != nil {
			http.Error(w, "unable to open user database", http.StatusInternalServerError)
			return
		}
		defer db.Close()

		path := strings.TrimPrefix(r.URL.Path, "/notes/")
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) == 1 && r.Method == http.MethodGet {
			noteID := parts[0]
			note, err := mgr.GetNoteByID(db, noteID)
			if err != nil || note == nil {
				http.Error(w, "note not found", http.StatusNotFound)
				return
			}
			backlinks, err := mgr.GetBacklinks(db, noteID)
			if err != nil {
				http.Error(w, "unable to get backlinks", http.StatusInternalServerError)
				return
			}
			noteResolver := func(title string) (string, bool, error) {
				n, err := mgr.GetNoteByTitle(db, title)
				if err != nil {
					return "", false, err
				}
				if n == nil {
					return "", false, nil
				}
				return n.ID, true, nil
			}
			htmlContent, err := markup.RenderMarkdownWithWikiLinks(note.Content, noteResolver)
			if err != nil {
				http.Error(w, "unable to render markdown", http.StatusInternalServerError)
				return
			}
			renderTemplate(w, "noteview.gohtml", map[string]interface{}{"Title": note.Title, "Body": template.HTML(htmlContent), "Backlinks": backlinks, "ID": note.ID})
			return
		}
		if len(parts) < 2 {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		noteID := parts[0]
		action := parts[1]

		switch action {
		case "edit":
			note, err := mgr.GetNoteByID(db, noteID)
			if err != nil || note == nil {
				http.Error(w, "note not found", http.StatusNotFound)
				return
			}

			type editData struct {
				ID           string
				Title        string
				Content      string
				Raw          string
				NotebookID   string
				Notebooks    []database.Notebook
				TitleError   string
				ContentError string
			}

			notebooks, err := mgr.ListNotebooks(db)
			if err != nil {
				notebooks = []database.Notebook{}
			}

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			templates.ExecuteTemplate(w, "note_item_edit_fragment", editData{ID: note.ID, Title: note.Title, Content: note.Content, Raw: note.Content, NotebookID: note.NotebookID, Notebooks: notebooks, TitleError: "", ContentError: ""})
		case "update":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			title := r.FormValue("title")
			content := r.FormValue("content")
			notebookID := r.FormValue("notebook_id")
			if title == "" || content == "" {
				data := map[string]interface{}{
					"ID":         noteID,
					"Title":      title,
					"Content":    content,
					"NotebookID": notebookID,
				}
				if title == "" {
					data["TitleError"] = "Title is required"
				}
				if content == "" {
					data["ContentError"] = "Content is required"
				}
				notebooks, err := mgr.ListNotebooks(db)
				if err != nil {
					notebooks = []database.Notebook{}
				}
				data["Notebooks"] = notebooks
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				templates.ExecuteTemplate(w, "note_item_edit_fragment", data)
				return
			}
			if err := mgr.UpdateNote(db, database.Note{ID: noteID, Title: title, Content: content, NotebookID: notebookID}); err != nil {
				http.Error(w, "unable to update note", http.StatusInternalServerError)
				return
			}

			titles := markup.ParseWikiLinks(content)
			targetIDs := []string{}
			for _, t := range titles {
				target, err := mgr.GetNoteByTitle(db, t)
				if err != nil {
					continue
				}
				if target != nil {
					targetIDs = append(targetIDs, target.ID)
				}
			}
			mgr.DeleteNoteLinks(db, noteID)
			mgr.InsertNoteLinks(db, noteID, targetIDs)

			tags := markup.ParseTags(content)
			mgr.InsertNoteTags(db, noteID, tags)

			note, err := mgr.GetNoteByID(db, noteID)
			if err != nil || note == nil {
				http.Error(w, "note not found after update", http.StatusInternalServerError)
				return
			}
			noteResolver := func(title string) (string, bool, error) {
				n, err := mgr.GetNoteByTitle(db, title)
				if err != nil {
					return "", false, err
				}
				if n == nil {
					return "", false, nil
				}
				return n.ID, true, nil
			}
			htmlContent, err := markup.RenderMarkdownWithWikiLinks(note.Content, noteResolver)
			if err != nil {
				http.Error(w, "unable to render markdown", http.StatusInternalServerError)
				return
			}
			if isHTMXRequest(r) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				templates.ExecuteTemplate(w, "note_item_fragment", map[string]interface{}{"ID": note.ID, "Title": note.Title, "UpdatedAt": note.UpdatedAt, "RenderedHTML": template.HTML(htmlContent)})
				return
			}
			http.Redirect(w, r, "/notes?msg=Note+saved", http.StatusSeeOther)
		case "delete":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if err := mgr.DeleteNote(db, noteID); err != nil {
				http.Error(w, "unable to delete note", http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, "/notes?msg=Note+deleted", http.StatusSeeOther)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}
}

func isHTMXRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

func renderNoteViewFragment(w http.ResponseWriter, note *database.Note, mgr *database.DatabaseManager, db *sql.DB) error {
	noteResolver := func(title string) (string, bool, error) {
		n, err := mgr.GetNoteByTitle(db, title)
		if err != nil {
			return "", false, err
		}
		if n == nil {
			return "", false, nil
		}
		return n.ID, true, nil
	}
	htmlContent, err := markup.RenderMarkdownWithWikiLinks(note.Content, noteResolver)
	if err != nil {
		return err
	}
	backlinks, err := mgr.GetBacklinks(db, note.ID)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return templates.ExecuteTemplate(w, "note_view_fragment", map[string]interface{}{"Title": note.Title, "Body": template.HTML(htmlContent), "Backlinks": backlinks, "ID": note.ID})
}

func viewNoteHandler(mgr *database.DatabaseManager, sessions *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r, sessions)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		title := r.URL.Query().Get("title")
		if title == "" {
			http.Error(w, "title is required", http.StatusBadRequest)
			return
		}

		db, err := mgr.OpenUserDB(userID)
		if err != nil {
			http.Error(w, "unable to open user database", http.StatusInternalServerError)
			return
		}
		defer db.Close()

		note, err := mgr.GetNoteByTitle(db, title)
		if err != nil {
			http.Error(w, "unable to get note", http.StatusInternalServerError)
			return
		}
		if note == nil {
			http.Redirect(w, r, "/notes?msg=Note+not+found", http.StatusSeeOther)
			return
		}

		if isHTMXRequest(r) {
			if err := renderNoteViewFragment(w, note, mgr, db); err != nil {
				http.Error(w, "unable to render note fragment", http.StatusInternalServerError)
			}
			return
		}

		noteResolver := func(title string) (string, bool, error) {
			n, err := mgr.GetNoteByTitle(db, title)
			if err != nil {
				return "", false, err
			}
			if n == nil {
				return "", false, nil
			}
			return n.ID, true, nil
		}
		htmlContent, err := markup.RenderMarkdownWithWikiLinks(note.Content, noteResolver)
		if err != nil {
			http.Error(w, "unable to render markdown", http.StatusInternalServerError)
			return
		}
		backlinks, err := mgr.GetBacklinks(db, note.ID)
		if err != nil {
			http.Error(w, "unable to get backlinks", http.StatusInternalServerError)
			return
		}
		renderTemplate(w, "noteview.gohtml", map[string]interface{}{"Title": note.Title, "Body": template.HTML(htmlContent), "Backlinks": backlinks, "ID": note.ID})
	}
}

func viewNoteEditHandler(mgr *database.DatabaseManager, sessions *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r, sessions)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		title := r.URL.Query().Get("title")
		if title == "" {
			http.Error(w, "title is required", http.StatusBadRequest)
			return
		}

		db, err := mgr.OpenUserDB(userID)
		if err != nil {
			http.Error(w, "unable to open user database", http.StatusInternalServerError)
			return
		}
		defer db.Close()

		note, err := mgr.GetNoteByTitle(db, title)
		if err != nil {
			http.Error(w, "unable to get note", http.StatusInternalServerError)
			return
		}
		if note == nil {
			http.Error(w, "note not found", http.StatusNotFound)
			return
		}

		data := struct {
			Title        string
			Raw          string
			ID           string
			NotebookID   string
			Notebooks    []database.Notebook
			TitleError   string
			ContentError string
		}{
			Title:        note.Title,
			Raw:          note.Content,
			ID:           note.ID,
			NotebookID:   note.NotebookID,
			TitleError:   "",
			ContentError: "",
		}

		notebooks, err := mgr.ListNotebooks(db)
		if err != nil {
			notebooks = []database.Notebook{}
		}
		data.Notebooks = notebooks

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		templates.ExecuteTemplate(w, "note_edit_fragment", data)
	}
}

func viewNoteUpdateHandler(mgr *database.DatabaseManager, sessions *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		userID, ok := currentUserID(r, sessions)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		originalTitle := r.FormValue("original_title")
		title := r.FormValue("title")
		content := r.FormValue("content")
		notebookID := r.FormValue("notebook_id")
		if title == "" || content == "" {
			data := struct {
				Title        string
				Raw          string
				NotebookID   string
				TitleError   string
				ContentError string
				Notebooks    []database.Notebook
			}{
				Title:      title,
				Raw:        content,
				NotebookID: notebookID,
			}
			if title == "" {
				data.TitleError = "Title is required"
			}
			if content == "" {
				data.ContentError = "Content is required"
			}
			tempDB, err := mgr.OpenUserDB(userID)
			if err == nil {
				defer tempDB.Close()
				notebooks, _ := mgr.ListNotebooks(tempDB)
				data.Notebooks = notebooks
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			templates.ExecuteTemplate(w, "note_edit_fragment", data)
			return
		}

		db, err := mgr.OpenUserDB(userID)
		if err != nil {
			http.Error(w, "unable to open user database", http.StatusInternalServerError)
			return
		}
		defer db.Close()

		note, err := mgr.GetNoteByTitle(db, originalTitle)
		if err != nil {
			http.Error(w, "unable to find note", http.StatusInternalServerError)
			return
		}
		if note == nil {
			http.Error(w, "note not found", http.StatusNotFound)
			return
		}

		if originalTitle != title {
			existing, err := mgr.GetNoteByTitle(db, title)
			if err != nil {
				http.Error(w, "unable to check existing title", http.StatusInternalServerError)
				return
			}
			if existing != nil {
				http.Error(w, "note title already exists", http.StatusConflict)
				return
			}
		}

		note.Title = title
		note.Content = content
		note.NotebookID = notebookID
		if err := mgr.UpdateNote(db, *note); err != nil {
			http.Error(w, "unable to update note", http.StatusInternalServerError)
			return
		}

		titles := markup.ParseWikiLinks(content)
		targetIDs := []string{}
		for _, t := range titles {
			target, err := mgr.GetNoteByTitle(db, t)
			if err != nil {
				continue
			}
			if target != nil {
				targetIDs = append(targetIDs, target.ID)
			}
		}
		mgr.DeleteNoteLinks(db, note.ID)
		mgr.InsertNoteLinks(db, note.ID, targetIDs)

		tags := markup.ParseTags(content)
		mgr.InsertNoteTags(db, note.ID, tags)

		if err := renderNoteViewFragment(w, note, mgr, db); err != nil {
			http.Error(w, "unable to render updated note", http.StatusInternalServerError)
			return
		}
	}
}

func searchHandler(mgr *database.DatabaseManager, sessions *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r, sessions)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		q := r.URL.Query().Get("q")
		if strings.TrimSpace(q) == "" {
			w.Write([]byte("<p>Start typing to search notes...</p>"))
			return
		}

		db, err := mgr.OpenUserDB(userID)
		if err != nil {
			http.Error(w, "unable to open user database", http.StatusInternalServerError)
			return
		}
		defer db.Close()

		notes, err := mgr.SearchNotes(db, q)
		if err != nil {
			http.Error(w, "unable to search notes", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if len(notes) == 0 {
			w.Write([]byte("<p>No matches found.</p>"))
			return
		}
		w.Write([]byte("<ul>"))
		for _, n := range notes {
			w.Write([]byte(fmt.Sprintf("<li><a href=\"/notes/view?title=%s\">%s</a></li>", url.QueryEscape(n.Title), html.EscapeString(n.Title))))
		}
		w.Write([]byte("</ul>"))
	}
}

func logoutHandler(sessions *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err == nil {
			sessions.DeleteSession(cookie.Value)
			http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", Expires: time.Unix(0, 0), MaxAge: -1})
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}

func createNotebookHandler(mgr *database.DatabaseManager, sessions *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r, sessions)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		name := r.FormValue("name")
		if name == "" {
			http.Redirect(w, r, "/notes?msg=Notebook+name+required", http.StatusSeeOther)
			return
		}

		db, err := mgr.OpenUserDB(userID)
		if err != nil {
			http.Error(w, "unable to open user database", http.StatusInternalServerError)
			return
		}
		defer db.Close()

		notebook := database.Notebook{ID: uuid.NewString(), Name: name}
		if err := mgr.CreateNotebook(db, notebook); err != nil {
			http.Error(w, "unable to create notebook", http.StatusInternalServerError)
			return
		}
		indexContent := "## Index\n\nWelcome to your new notebook. Use headings to create chapters.\n\n### Chapter 1\n[[Write your first note here]]\n\n#index"

		indexNote := database.Note{
			ID:         uuid.NewString(),
			Title:      "Index - " + name,
			Content:    indexContent,
			NotebookID: notebook.ID,
		}

		if err := mgr.CreateNote(db, indexNote); err != nil {
			http.Error(w, "unable to create index note", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/notes?msg=Notebook+created", http.StatusSeeOther)
	}
}

func notebookViewHandler(mgr *database.DatabaseManager, sessions *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := currentUserID(r, sessions)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		notebookID := strings.TrimPrefix(r.URL.Path, "/notebooks/")
		notebookID = strings.TrimSuffix(notebookID, "/")

		db, err := mgr.OpenUserDB(userID)
		if err != nil {
			http.Error(w, "unable to open user database", http.StatusInternalServerError)
			return
		}
		defer db.Close()

		notebook, err := mgr.GetNotebookByID(db, notebookID)
		if err != nil || notebook == nil {
			http.Error(w, "notebook not found", http.StatusNotFound)
			return
		}

		notes, err := mgr.GetNotesByNotebookID(db, notebookID)
		if err != nil {
			http.Error(w, "unable to get notes", http.StatusInternalServerError)
			return
		}

		type RenderNote struct {
			ID           string
			Title        string
			Content      string
			UpdatedAt    string
			RenderedHTML template.HTML
			NotebookID   string
		}

		noteExists := func(title string) (string, bool, error) {
			n, err := mgr.GetNoteByTitle(db, title)
			if err != nil {
				return "", false, err
			}
			if n == nil {
				return "", false, nil
			}
			return n.ID, true, nil
		}

		rendered := []RenderNote{}
		for _, note := range notes {
			htmlContent, err := markup.RenderMarkdownWithWikiLinks(note.Content, noteExists)
			if err != nil {
				http.Error(w, "unable to render markdown", http.StatusInternalServerError)
				return
			}
			rendered = append(rendered, RenderNote{
				ID:           note.ID,
				Title:        note.Title,
				Content:      note.Content,
				UpdatedAt:    note.UpdatedAt,
				RenderedHTML: template.HTML(htmlContent),
				NotebookID:   note.NotebookID,
			})
		}

		notebooks, err := mgr.ListNotebooks(db)
		if err != nil {
			notebooks = []database.Notebook{}
		}

		renderTemplate(w, "notes.gohtml", map[string]interface{}{"Notes": rendered, "Notebooks": notebooks, "Notebook": notebook})
	}
}

func main() {
	mgr := database.NewManager("./storage")
	systemDB, err := mgr.InitSystemDB()
	if err != nil {
		log.Fatalf("failed to init system db: %v", err)
	}
	defer systemDB.Close()

	sessions := NewSessionStore()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		renderTemplate(w, "home.gohtml", nil)
	})
	http.HandleFunc("/signup", signupHandler(mgr, systemDB, sessions))
	http.HandleFunc("/login", loginHandler(mgr, systemDB, sessions))
	http.HandleFunc("/notes", notesHandler(mgr, sessions))
	http.HandleFunc("/notes/create", createNoteHandler(mgr, sessions))
	http.HandleFunc("/notebooks/create", createNotebookHandler(mgr, sessions))
	http.HandleFunc("/notebooks/", notebookViewHandler(mgr, sessions))
	http.HandleFunc("/notes/view", viewNoteHandler(mgr, sessions))
	http.HandleFunc("/notes/view/edit", viewNoteEditHandler(mgr, sessions))
	http.HandleFunc("/notes/view/update", viewNoteUpdateHandler(mgr, sessions))
	http.HandleFunc("/notes/", noteActionHandler(mgr, sessions))
	http.HandleFunc("/search", searchHandler(mgr, sessions))
	http.HandleFunc("/logout", logoutHandler(sessions))

	addr := ":8080"
	log.Printf("starting server on %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
