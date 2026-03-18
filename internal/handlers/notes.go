package handlers

import (
	"database/sql"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/felan/blindsidian/internal/database"
	"github.com/felan/blindsidian/internal/markup"
	"github.com/google/uuid"
)

func (s *Server) RenderTemplate(w http.ResponseWriter, name string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.Templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "unable to render page", http.StatusInternalServerError)
	}
}

func (s *Server) NotesHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUserID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	db, err := s.DBManager.OpenUserDB(userID)
	if err != nil {
		http.Error(w, "unable to open user database", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	notes, err := s.DBManager.ListNotes(db)
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
		n, err := s.DBManager.GetNoteByTitle(db, title)
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
	notebooks, err := s.DBManager.ListNotebooks(db)
	if err != nil {
		notebooks = []database.Notebook{}
	}
	s.RenderTemplate(w, "notes.gohtml", map[string]interface{}{"Notes": rendered, "Message": message, "CreateTitle": createTitle, "Notebooks": notebooks})
}

func (s *Server) CreateNoteHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUserID(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method == http.MethodGet {
		createTitle := r.URL.Query().Get("title")
		s.RenderTemplate(w, "notes.gohtml", map[string]interface{}{"CreateTitle": createTitle})
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	title := r.FormValue("title")
	content := r.FormValue("content")
	if title == "" || content == "" {
		db, err := s.DBManager.OpenUserDB(userID)
		if err != nil {
			http.Error(w, "unable to open user database", http.StatusInternalServerError)
			return
		}
		defer db.Close()

		notes, err := s.DBManager.ListNotes(db)
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
			n, err := s.DBManager.GetNoteByTitle(db, title)
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
		s.RenderTemplate(w, "notes.gohtml", data)
		return
	}

	db, err := s.DBManager.OpenUserDB(userID)
	if err != nil {
		http.Error(w, "unable to open user database", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	existing, err := s.DBManager.GetNoteByTitle(db, title)
	if err != nil {
		http.Error(w, "unable to check existing note", http.StatusInternalServerError)
		return
	}
	if existing != nil {
		http.Error(w, "note title already exists", http.StatusConflict)
		return
	}

	note := database.Note{ID: uuid.NewString(), Title: title, Content: content, NotebookID: r.FormValue("notebook_id")}
	if err := s.DBManager.CreateNote(db, note); err != nil {
		http.Error(w, "unable to create note", http.StatusInternalServerError)
		return
	}

	titles := markup.ParseWikiLinks(note.Content)
	targetIDs := []string{}
	for _, t := range titles {
		target, err := s.DBManager.GetNoteByTitle(db, t)
		if err != nil {
			continue
		}
		if target != nil {
			targetIDs = append(targetIDs, target.ID)
		}
	}
	s.DBManager.InsertNoteLinks(db, note.ID, targetIDs)

	tags := markup.ParseTags(note.Content)
	s.DBManager.InsertNoteTags(db, note.ID, tags)

	http.Redirect(w, r, "/notes?msg=Note+saved", http.StatusSeeOther)
}

func (s *Server) NoteActionHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUserID(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	db, err := s.DBManager.OpenUserDB(userID)
	if err != nil {
		http.Error(w, "unable to open user database", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	path := strings.TrimPrefix(r.URL.Path, "/notes/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 1 && r.Method == http.MethodGet {
		noteID := parts[0]
		note, err := s.DBManager.GetNoteByID(db, noteID)
		if err != nil || note == nil {
			http.Error(w, "note not found", http.StatusNotFound)
			return
		}
		backlinks, err := s.DBManager.GetBacklinks(db, noteID)
		if err != nil {
			http.Error(w, "unable to get backlinks", http.StatusInternalServerError)
			return
		}
		noteResolver := func(title string) (string, bool, error) {
			n, err := s.DBManager.GetNoteByTitle(db, title)
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
		s.RenderTemplate(w, "noteview.gohtml", map[string]interface{}{"Title": note.Title, "Body": template.HTML(htmlContent), "Backlinks": backlinks, "ID": note.ID})
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
		note, err := s.DBManager.GetNoteByID(db, noteID)
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

		notebooks, err := s.DBManager.ListNotebooks(db)
		if err != nil {
			notebooks = []database.Notebook{}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		s.Templates.ExecuteTemplate(w, "note_item_edit_fragment", editData{ID: note.ID, Title: note.Title, Content: note.Content, Raw: note.Content, NotebookID: note.NotebookID, Notebooks: notebooks, TitleError: "", ContentError: ""})
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
			notebooks, err := s.DBManager.ListNotebooks(db)
			if err != nil {
				notebooks = []database.Notebook{}
			}
			data["Notebooks"] = notebooks
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			s.Templates.ExecuteTemplate(w, "note_item_edit_fragment", data)
			return
		}
		if err := s.DBManager.UpdateNote(db, database.Note{ID: noteID, Title: title, Content: content, NotebookID: notebookID}); err != nil {
			http.Error(w, "unable to update note", http.StatusInternalServerError)
			return
		}

		titles := markup.ParseWikiLinks(content)
		targetIDs := []string{}
		for _, t := range titles {
			target, err := s.DBManager.GetNoteByTitle(db, t)
			if err != nil {
				continue
			}
			if target != nil {
				targetIDs = append(targetIDs, target.ID)
			}
		}
		s.DBManager.DeleteNoteLinks(db, noteID)
		s.DBManager.InsertNoteLinks(db, noteID, targetIDs)

		tags := markup.ParseTags(content)
		s.DBManager.InsertNoteTags(db, noteID, tags)

		note, err := s.DBManager.GetNoteByID(db, noteID)
		if err != nil || note == nil {
			http.Error(w, "note not found after update", http.StatusInternalServerError)
			return
		}
		noteResolver := func(title string) (string, bool, error) {
			n, err := s.DBManager.GetNoteByTitle(db, title)
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
		if s.isHTMXRequest(r) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			s.Templates.ExecuteTemplate(w, "note_item_fragment", map[string]interface{}{"ID": note.ID, "Title": note.Title, "UpdatedAt": note.UpdatedAt, "RenderedHTML": template.HTML(htmlContent)})
			return
		}
		http.Redirect(w, r, "/notes?msg=Note+saved", http.StatusSeeOther)
	case "delete":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := s.DBManager.DeleteNote(db, noteID); err != nil {
			http.Error(w, "unable to delete note", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/notes?msg=Note+deleted", http.StatusSeeOther)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (s *Server) isHTMXRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

func (s *Server) renderNoteViewFragment(w http.ResponseWriter, note *database.Note, db *sql.DB) error {
	noteResolver := func(title string) (string, bool, error) {
		n, err := s.DBManager.GetNoteByTitle(db, title)
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
	backlinks, err := s.DBManager.GetBacklinks(db, note.ID)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return s.Templates.ExecuteTemplate(w, "note_view_fragment", map[string]interface{}{"Title": note.Title, "Body": template.HTML(htmlContent), "Backlinks": backlinks, "ID": note.ID})
}

func (s *Server) ViewNoteHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUserID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	title := r.URL.Query().Get("title")
	if title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}

	db, err := s.DBManager.OpenUserDB(userID)
	if err != nil {
		http.Error(w, "unable to open user database", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	note, err := s.DBManager.GetNoteByTitle(db, title)
	if err != nil {
		http.Error(w, "unable to get note", http.StatusInternalServerError)
		return
	}
	if note == nil {
		http.Redirect(w, r, "/notes?msg=Note+not+found", http.StatusSeeOther)
		return
	}

	if s.isHTMXRequest(r) {
		if err := s.renderNoteViewFragment(w, note, db); err != nil {
			http.Error(w, "unable to render note fragment", http.StatusInternalServerError)
		}
		return
	}

	noteResolver := func(title string) (string, bool, error) {
		n, err := s.DBManager.GetNoteByTitle(db, title)
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
	backlinks, err := s.DBManager.GetBacklinks(db, note.ID)
	if err != nil {
		http.Error(w, "unable to get backlinks", http.StatusInternalServerError)
		return
	}
	s.RenderTemplate(w, "noteview.gohtml", map[string]interface{}{"Title": note.Title, "Body": template.HTML(htmlContent), "Backlinks": backlinks, "ID": note.ID})
}

func (s *Server) ViewNoteEditHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUserID(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	title := r.URL.Query().Get("title")
	if title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}

	db, err := s.DBManager.OpenUserDB(userID)
	if err != nil {
		http.Error(w, "unable to open user database", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	note, err := s.DBManager.GetNoteByTitle(db, title)
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

	notebooks, err := s.DBManager.ListNotebooks(db)
	if err != nil {
		notebooks = []database.Notebook{}
	}
	data.Notebooks = notebooks

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.Templates.ExecuteTemplate(w, "note_edit_fragment", data)
}

func (s *Server) ViewNoteUpdateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID, ok := s.currentUserID(r)
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
		tempDB, err := s.DBManager.OpenUserDB(userID)
		if err == nil {
			defer tempDB.Close()
			notebooks, _ := s.DBManager.ListNotebooks(tempDB)
			data.Notebooks = notebooks
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		s.Templates.ExecuteTemplate(w, "note_edit_fragment", data)
		return
	}

	db, err := s.DBManager.OpenUserDB(userID)
	if err != nil {
		http.Error(w, "unable to open user database", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	note, err := s.DBManager.GetNoteByTitle(db, originalTitle)
	if err != nil {
		http.Error(w, "unable to find note", http.StatusInternalServerError)
		return
	}
	if note == nil {
		http.Error(w, "note not found", http.StatusNotFound)
		return
	}

	if originalTitle != title {
		existing, err := s.DBManager.GetNoteByTitle(db, title)
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
	if err := s.DBManager.UpdateNote(db, *note); err != nil {
		http.Error(w, "unable to update note", http.StatusInternalServerError)
		return
	}

	titles := markup.ParseWikiLinks(content)
	targetIDs := []string{}
	for _, t := range titles {
		target, err := s.DBManager.GetNoteByTitle(db, t)
		if err != nil {
			continue
		}
		if target != nil {
			targetIDs = append(targetIDs, target.ID)
		}
	}
	s.DBManager.DeleteNoteLinks(db, note.ID)
	s.DBManager.InsertNoteLinks(db, note.ID, targetIDs)

	tags := markup.ParseTags(content)
	s.DBManager.InsertNoteTags(db, note.ID, tags)

	if err := s.renderNoteViewFragment(w, note, db); err != nil {
		http.Error(w, "unable to render updated note", http.StatusInternalServerError)
		return
	}
}

func (s *Server) SearchHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUserID(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	q := r.URL.Query().Get("q")
	if strings.TrimSpace(q) == "" {
		w.Write([]byte("<p>Start typing to search notes...</p>"))
		return
	}

	db, err := s.DBManager.OpenUserDB(userID)
	if err != nil {
		http.Error(w, "unable to open user database", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	notes, err := s.DBManager.SearchNotes(db, q)
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
