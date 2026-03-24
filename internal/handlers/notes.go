package handlers

import (
	"database/sql"
	"fmt"
	"html"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/flanfranchi1/notty/internal/database"
	"github.com/flanfranchi1/notty/internal/i18n"
	"github.com/flanfranchi1/notty/internal/markup"
	"github.com/google/uuid"
)

// RenderTemplate executes a named template, automatically injecting the
// resolved locale tag ("Locale") and the corresponding translation map ("T")
// into the template data so every page can use {{.Locale}} and {{index .T "key"}}.
func (s *Server) RenderTemplate(w http.ResponseWriter, r *http.Request, name string, data interface{}) {
	// Resolve locale and translations from the request context.
	locale := i18n.LocaleFromContext(r.Context())
	var translations map[string]string
	if s.Bundle != nil {
		translations = s.Bundle.Translations(locale)
	} else {
		translations = map[string]string{}
	}

	// Normalise data into map[string]interface{} so we can inject extra keys.
	td := make(map[string]interface{})
	switch d := data.(type) {
	case map[string]interface{}:
		for k, v := range d {
			td[k] = v
		}
	case map[string]string:
		for k, v := range d {
			td[k] = v
		}
	}
	td["Locale"] = locale
	td["T"] = translations

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.Templates.ExecuteTemplate(w, name, td); err != nil {
		log.Printf("RenderTemplate (%s): ExecuteTemplate: %v", name, err)
		http.Error(w, s.t(r, "error.render_failed"), http.StatusInternalServerError)
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
		log.Printf("NotesHandler: OpenUserDB: %v", err)
		http.Error(w, s.t(r, "error.db_unavailable"), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	notes, err := s.DBManager.ListNotes(db)
	if err != nil {
		log.Printf("NotesHandler: ListNotes: %v", err)
		http.Error(w, s.t(r, "error.internal"), http.StatusInternalServerError)
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
			log.Printf("NotesHandler: RenderMarkdownWithWikiLinks: %v", err)
			http.Error(w, s.t(r, "error.internal"), http.StatusInternalServerError)
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
	inboxCount, _ := s.DBManager.CountInboxNotes(db)
	allTags, _ := s.DBManager.ListAllTags(db)
	s.RenderTemplate(w, r, "notes.gohtml", map[string]interface{}{"Notes": rendered, "Message": message, "CreateTitle": createTitle, "Notebooks": notebooks, "InboxCount": inboxCount, "AllTags": allTags})
}

// InboxHandler serves GET /inbox — all notes that are not assigned to any
// notebook.  Uses the same notes.gohtml template with IsInbox=true so the
// sidebar and heading text switch to "Inbox" context.
func (s *Server) InboxHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUserID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	db, err := s.DBManager.OpenUserDB(userID)
	if err != nil {
		log.Printf("InboxHandler: OpenUserDB: %v", err)
		http.Error(w, s.t(r, "error.db_unavailable"), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	notes, err := s.DBManager.ListInboxNotes(db)
	if err != nil {
		log.Printf("InboxHandler: ListInboxNotes: %v", err)
		http.Error(w, s.t(r, "error.internal"), http.StatusInternalServerError)
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
			log.Printf("InboxHandler: RenderMarkdownWithWikiLinks: %v", err)
			http.Error(w, s.t(r, "error.internal"), http.StatusInternalServerError)
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

	notebooks, err := s.DBManager.ListNotebooks(db)
	if err != nil {
		notebooks = []database.Notebook{}
	}
	inboxCount := len(rendered)
	allTags, _ := s.DBManager.ListAllTags(db)
	s.RenderTemplate(w, r, "notes.gohtml", map[string]interface{}{
		"Notes":      rendered,
		"Notebooks":  notebooks,
		"InboxCount": inboxCount,
		"IsInbox":    true,
		"AllTags":    allTags,
	})
}

func (s *Server) CreateNoteHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUserID(r)
	if !ok {
		http.Error(w, s.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}

	if r.Method == http.MethodGet {
		createTitle := r.URL.Query().Get("title")
		s.RenderTemplate(w, r, "notes.gohtml", map[string]interface{}{"CreateTitle": createTitle})
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, s.t(r, "error.method_not_allowed"), http.StatusMethodNotAllowed)
		return
	}

	title := r.FormValue("title")
	content := r.FormValue("content")
	if title == "" || content == "" {
		db, err := s.DBManager.OpenUserDB(userID)
		if err != nil {
			log.Printf("CreateNoteHandler: OpenUserDB (validation): %v", err)
			http.Error(w, s.t(r, "error.db_unavailable"), http.StatusInternalServerError)
			return
		}
		defer db.Close()

		notes, err := s.DBManager.ListNotes(db)
		if err != nil {
			log.Printf("CreateNoteHandler: ListNotes: %v", err)
			http.Error(w, s.t(r, "error.internal"), http.StatusInternalServerError)
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
				log.Printf("CreateNoteHandler: RenderMarkdownWithWikiLinks: %v", err)
				http.Error(w, s.t(r, "error.internal"), http.StatusInternalServerError)
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
		s.RenderTemplate(w, r, "notes.gohtml", data)
		return
	}

	db, err := s.DBManager.OpenUserDB(userID)
	if err != nil {
		log.Printf("CreateNoteHandler: OpenUserDB: %v", err)
		http.Error(w, s.t(r, "error.db_unavailable"), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	existing, err := s.DBManager.GetNoteByTitle(db, title)
	if err != nil {
		log.Printf("CreateNoteHandler: GetNoteByTitle (existing): %v", err)
		http.Error(w, s.t(r, "error.internal"), http.StatusInternalServerError)
		return
	}
	if existing != nil {
		http.Error(w, s.t(r, "error.note_title_conflict"), http.StatusConflict)
		return
	}

	note := database.Note{ID: uuid.NewString(), Title: title, Content: content, NotebookID: r.FormValue("notebook_id")}
	if err := s.DBManager.CreateNote(db, note); err != nil {
		log.Printf("CreateNoteHandler: CreateNote: %v", err)
		http.Error(w, s.t(r, "error.internal"), http.StatusInternalServerError)
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

	noteURL := "/notes/view?title=" + url.QueryEscape(note.Title)
	if s.isHTMXRequest(r) {
		w.Header().Set("HX-Redirect", noteURL)
		w.WriteHeader(http.StatusOK)
		return
	}

	http.Redirect(w, r, noteURL, http.StatusSeeOther)
}

func (s *Server) NoteActionHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUserID(r)
	if !ok {
		http.Error(w, s.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}

	db, err := s.DBManager.OpenUserDB(userID)
	if err != nil {
		log.Printf("NoteActionHandler: OpenUserDB: %v", err)
		http.Error(w, s.t(r, "error.db_unavailable"), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	path := strings.TrimPrefix(r.URL.Path, "/notes/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 1 && r.Method == http.MethodGet {
		noteID := parts[0]
		note, err := s.DBManager.GetNoteByID(db, noteID)
		if err != nil {
			log.Printf("NoteActionHandler: GetNoteByID: %v", err)
			http.Error(w, s.t(r, "error.note_not_found"), http.StatusNotFound)
			return
		}
		if note == nil {
			http.Error(w, s.t(r, "error.note_not_found"), http.StatusNotFound)
			return
		}
		backlinks, err := s.DBManager.GetBacklinks(db, noteID)
		if err != nil {
			log.Printf("NoteActionHandler: GetBacklinks: %v", err)
			http.Error(w, s.t(r, "error.internal"), http.StatusInternalServerError)
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
			log.Printf("NoteActionHandler: RenderMarkdownWithWikiLinks: %v", err)
			http.Error(w, s.t(r, "error.internal"), http.StatusInternalServerError)
			return
		}
		tags, _ := s.DBManager.GetTagsByNoteID(db, noteID)
		s.RenderTemplate(w, r, "noteview.gohtml", map[string]interface{}{"Title": note.Title, "Body": template.HTML(htmlContent), "Backlinks": backlinks, "ID": note.ID, "Tags": tags})
		return
	}
	if len(parts) < 2 {
		http.Error(w, s.t(r, "error.not_found"), http.StatusNotFound)
		return
	}

	noteID := parts[0]
	action := parts[1]

	switch action {
	case "edit":
		note, err := s.DBManager.GetNoteByID(db, noteID)
		if err != nil {
			log.Printf("NoteActionHandler edit: GetNoteByID: %v", err)
			http.Error(w, s.t(r, "error.note_not_found"), http.StatusNotFound)
			return
		}
		if note == nil {
			http.Error(w, s.t(r, "error.note_not_found"), http.StatusNotFound)
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

		s.renderFragment(w, r, "note_item_edit_fragment", map[string]interface{}{"ID": note.ID, "Title": note.Title, "Content": note.Content, "Raw": note.Content, "NotebookID": note.NotebookID, "Notebooks": notebooks, "TitleError": "", "ContentError": ""})
	case "update":
		if r.Method != http.MethodPost {
			http.Error(w, s.t(r, "error.method_not_allowed"), http.StatusMethodNotAllowed)
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
			s.renderFragment(w, r, "note_item_edit_fragment", data)
			return
		}
		if err := s.DBManager.UpdateNote(db, database.Note{ID: noteID, Title: title, Content: content, NotebookID: notebookID}); err != nil {
			log.Printf("NoteActionHandler update: UpdateNote: %v", err)
			http.Error(w, s.t(r, "error.internal"), http.StatusInternalServerError)
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
		if err != nil {
			log.Printf("NoteActionHandler update: GetNoteByID (after update): %v", err)
			http.Error(w, s.t(r, "error.note_not_found"), http.StatusInternalServerError)
			return
		}
		if note == nil {
			http.Error(w, s.t(r, "error.note_not_found"), http.StatusInternalServerError)
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
			log.Printf("NoteActionHandler update: RenderMarkdownWithWikiLinks: %v", err)
			http.Error(w, s.t(r, "error.internal"), http.StatusInternalServerError)
			return
		}
		if s.isHTMXRequest(r) {
			// If the request originated from the Inbox view and the note has just been
			// assigned to a notebook, remove the item from the inbox list entirely.
			currentURL := r.Header.Get("HX-Current-URL")
			fromInbox := strings.Contains(currentURL, "/inbox")
			if fromInbox && notebookID != "" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("HX-Reswap", "delete")
				return
			}
			s.renderFragment(w, r, "note_item_fragment", map[string]interface{}{"ID": note.ID, "Title": note.Title, "UpdatedAt": note.UpdatedAt, "RenderedHTML": template.HTML(htmlContent)})
			return
		}
		http.Redirect(w, r, "/notes?msg=Note+saved", http.StatusSeeOther)

	case "autosave":
		// Lightweight background save triggered by the editor's auto-save form.
		// Returns a small HTML status indicator only (no page navigation).
		if r.Method != http.MethodPost {
			http.Error(w, s.t(r, "error.method_not_allowed"), http.StatusMethodNotAllowed)
			return
		}
		title := strings.TrimSpace(r.FormValue("title"))
		content := r.FormValue("content")
		notebookID := r.FormValue("notebook_id")

		// Server-side title fallback: scan for the first ATX heading.
		if title == "" {
			for _, line := range strings.SplitN(content, "\n", 30) {
				if strings.HasPrefix(line, "# ") {
					title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
					break
				}
			}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		if title == "" {
			// Cannot save without a title; ask the user to add a heading.
			fmt.Fprint(w, `<span class="save-status save-status--warn">`+
				html.EscapeString(s.t(r, "note.save_untitled"))+`</span>`)
			return
		}

		existing, err := s.DBManager.GetNoteByID(db, noteID)
		if err != nil || existing == nil {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `<span class="save-status save-status--err">`+
				html.EscapeString(s.t(r, "note.save_failed"))+`</span>`)
			return
		}

		existing.Title = title
		existing.Content = content
		existing.NotebookID = notebookID

		if err := s.DBManager.UpdateNote(db, *existing); err != nil {
			log.Printf("NoteActionHandler autosave: UpdateNote: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `<span class="save-status save-status--err">`+
				html.EscapeString(s.t(r, "note.save_failed"))+`</span>`)
			return
		}

		wikiTitles := markup.ParseWikiLinks(content)
		targetIDs := []string{}
		for _, t := range wikiTitles {
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
		s.DBManager.InsertNoteTags(db, noteID, markup.ParseTags(content))

		fmt.Fprint(w, `<span class="save-status save-status--ok">`+
			html.EscapeString(s.t(r, "note.saved"))+`</span>`)

	case "preview":
		// Render raw Markdown POSTed from the editor into HTML for the preview pane.
		if r.Method != http.MethodPost {
			http.Error(w, s.t(r, "error.method_not_allowed"), http.StatusMethodNotAllowed)
			return
		}
		content := r.FormValue("content")
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
		htmlContent, err := markup.RenderMarkdownWithWikiLinks(content, noteResolver)
		if err != nil {
			log.Printf("NoteActionHandler preview: RenderMarkdownWithWikiLinks: %v", err)
			http.Error(w, s.t(r, "error.internal"), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, string(htmlContent))
	case "delete":
		if r.Method != http.MethodPost {
			http.Error(w, s.t(r, "error.method_not_allowed"), http.StatusMethodNotAllowed)
			return
		}
		if err := s.DBManager.DeleteNote(db, noteID); err != nil {
			log.Printf("NoteActionHandler delete: DeleteNote: %v", err)
			http.Error(w, s.t(r, "error.internal"), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/notes?msg=Note+deleted", http.StatusSeeOther)
	default:
		http.Error(w, s.t(r, "error.not_found"), http.StatusNotFound)
	}
}

func (s *Server) isHTMXRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// t returns the translated string for key in the current request's locale.
// Falls back to the key name itself so missing translations are always visible.
func (s *Server) t(r *http.Request, key string) string {
	locale := i18n.LocaleFromContext(r.Context())
	if s.Bundle != nil {
		if msgs := s.Bundle.Translations(locale); msgs != nil {
			if v, ok := msgs[key]; ok {
				return v
			}
		}
	}
	return key
}

// renderFragment executes a partial (HTMX) template with the same locale and
// translation map that RenderTemplate injects into full-page templates.
func (s *Server) renderFragment(w http.ResponseWriter, r *http.Request, name string, data map[string]interface{}) {
	locale := i18n.LocaleFromContext(r.Context())
	var translations map[string]string
	if s.Bundle != nil {
		translations = s.Bundle.Translations(locale)
	} else {
		translations = map[string]string{}
	}
	if data == nil {
		data = make(map[string]interface{})
	}
	data["Locale"] = locale
	data["T"] = translations
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.Templates.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("renderFragment (%s): %v", name, err)
	}
}

func (s *Server) renderNoteViewFragment(w http.ResponseWriter, r *http.Request, note *database.Note, db *sql.DB) error {
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
	tags, err := s.DBManager.GetTagsByNoteID(db, note.ID)
	if err != nil {
		tags = nil
	}
	s.renderFragment(w, r, "note_view_fragment", map[string]interface{}{"Title": note.Title, "Body": template.HTML(htmlContent), "Backlinks": backlinks, "ID": note.ID, "Tags": tags})
	return nil
}

func (s *Server) ViewNoteHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUserID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	title := r.URL.Query().Get("title")
	if title == "" {
		http.Error(w, s.t(r, "error.title_required"), http.StatusBadRequest)
		return
	}

	db, err := s.DBManager.OpenUserDB(userID)
	if err != nil {
		log.Printf("ViewNoteHandler: OpenUserDB: %v", err)
		http.Error(w, s.t(r, "error.db_unavailable"), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	note, err := s.DBManager.GetNoteByTitle(db, title)
	if err != nil {
		log.Printf("ViewNoteHandler: GetNoteByTitle: %v", err)
		http.Error(w, s.t(r, "error.note_not_found"), http.StatusInternalServerError)
		return
	}
	if note == nil {
		http.Redirect(w, r, "/notes?msg=Note+not+found", http.StatusSeeOther)
		return
	}

	if s.isHTMXRequest(r) {
		if err := s.renderNoteViewFragment(w, r, note, db); err != nil {
			log.Printf("ViewNoteHandler: renderNoteViewFragment: %v", err)
			http.Error(w, s.t(r, "error.render_failed"), http.StatusInternalServerError)
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
		log.Printf("ViewNoteHandler: RenderMarkdownWithWikiLinks: %v", err)
		http.Error(w, s.t(r, "error.internal"), http.StatusInternalServerError)
		return
	}
	backlinks, err := s.DBManager.GetBacklinks(db, note.ID)
	if err != nil {
		log.Printf("ViewNoteHandler: GetBacklinks: %v", err)
		http.Error(w, s.t(r, "error.internal"), http.StatusInternalServerError)
		return
	}
	tags, _ := s.DBManager.GetTagsByNoteID(db, note.ID)
	s.RenderTemplate(w, r, "noteview.gohtml", map[string]interface{}{"Title": note.Title, "Body": template.HTML(htmlContent), "Backlinks": backlinks, "ID": note.ID, "Tags": tags})
}

func (s *Server) ViewNoteEditHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUserID(r)
	if !ok {
		http.Error(w, s.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}

	title := r.URL.Query().Get("title")
	if title == "" {
		http.Error(w, s.t(r, "error.title_required"), http.StatusBadRequest)
		return
	}

	db, err := s.DBManager.OpenUserDB(userID)
	if err != nil {
		log.Printf("ViewNoteEditHandler: OpenUserDB: %v", err)
		http.Error(w, s.t(r, "error.db_unavailable"), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	note, err := s.DBManager.GetNoteByTitle(db, title)
	if err != nil {
		log.Printf("ViewNoteEditHandler: GetNoteByTitle: %v", err)
		http.Error(w, s.t(r, "error.note_not_found"), http.StatusInternalServerError)
		return
	}
	if note == nil {
		http.Error(w, s.t(r, "error.note_not_found"), http.StatusNotFound)
		return
	}

	notebooks, err := s.DBManager.ListNotebooks(db)
	if err != nil {
		notebooks = []database.Notebook{}
	}

	s.renderFragment(w, r, "note_edit_fragment", map[string]interface{}{
		"Title":      note.Title,
		"Raw":        note.Content,
		"ID":         note.ID,
		"NotebookID": note.NotebookID,
		"Notebooks":  notebooks,
		"TitleError": "", "ContentError": "",
	})
}

func (s *Server) ViewNoteUpdateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, s.t(r, "error.method_not_allowed"), http.StatusMethodNotAllowed)
		return
	}

	userID, ok := s.currentUserID(r)
	if !ok {
		http.Error(w, s.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}

	originalTitle := r.FormValue("original_title")
	title := r.FormValue("title")
	content := r.FormValue("content")
	notebookID := r.FormValue("notebook_id")
	if title == "" || content == "" {
		titleErr, contentErr := "", ""
		if title == "" {
			titleErr = "Title is required"
		}
		if content == "" {
			contentErr = "Content is required"
		}
		var notebooks []database.Notebook
		tempDB, err := s.DBManager.OpenUserDB(userID)
		if err == nil {
			defer tempDB.Close()
			notebooks, _ = s.DBManager.ListNotebooks(tempDB)
		}
		s.renderFragment(w, r, "note_edit_fragment", map[string]interface{}{
			"Title":        title,
			"Raw":          content,
			"NotebookID":   notebookID,
			"TitleError":   titleErr,
			"ContentError": contentErr,
			"Notebooks":    notebooks,
		})
		return
	}

	db, err := s.DBManager.OpenUserDB(userID)
	if err != nil {
		log.Printf("ViewNoteUpdateHandler: OpenUserDB: %v", err)
		http.Error(w, s.t(r, "error.db_unavailable"), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	note, err := s.DBManager.GetNoteByTitle(db, originalTitle)
	if err != nil {
		log.Printf("ViewNoteUpdateHandler: GetNoteByTitle (original): %v", err)
		http.Error(w, s.t(r, "error.note_not_found"), http.StatusInternalServerError)
		return
	}
	if note == nil {
		http.Error(w, s.t(r, "error.note_not_found"), http.StatusNotFound)
		return
	}

	if originalTitle != title {
		existing, err := s.DBManager.GetNoteByTitle(db, title)
		if err != nil {
			log.Printf("ViewNoteUpdateHandler: GetNoteByTitle (conflict): %v", err)
			http.Error(w, s.t(r, "error.internal"), http.StatusInternalServerError)
			return
		}
		if existing != nil {
			http.Error(w, s.t(r, "error.note_title_conflict"), http.StatusConflict)
			return
		}
	}

	note.Title = title
	note.Content = content
	note.NotebookID = notebookID
	if err := s.DBManager.UpdateNote(db, *note); err != nil {
		log.Printf("ViewNoteUpdateHandler: UpdateNote: %v", err)
		http.Error(w, s.t(r, "error.internal"), http.StatusInternalServerError)
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

	if err := s.renderNoteViewFragment(w, r, note, db); err != nil {
		log.Printf("ViewNoteUpdateHandler: renderNoteViewFragment: %v", err)
		http.Error(w, s.t(r, "error.render_failed"), http.StatusInternalServerError)
		return
	}
}

func (s *Server) SearchHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUserID(r)
	if !ok {
		http.Error(w, s.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}

	q := r.URL.Query().Get("q")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if strings.TrimSpace(q) == "" {
		fmt.Fprint(w, `<div id="search-announcer" class="sr-only" aria-live="polite" aria-atomic="true" hx-swap-oob="true"></div>`)
		return
	}

	db, err := s.DBManager.OpenUserDB(userID)
	if err != nil {
		log.Printf("SearchHandler: OpenUserDB: %v", err)
		http.Error(w, s.t(r, "error.db_unavailable"), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	isCmdPalette := r.Header.Get("HX-Target") == "cmd-results"

	if strings.HasPrefix(q, "#") {
		tag := strings.ToLower(strings.TrimPrefix(q, "#"))
		results, err := s.DBManager.GetNotesByTag(db, tag)
		if err != nil {
			log.Printf("SearchHandler: GetNotesByTag: %v", err)
			http.Error(w, s.t(r, "error.internal"), http.StatusInternalServerError)
			return
		}
		count := len(results)
		announcement := fmt.Sprintf("%d result", count)
		if count != 1 {
			announcement += "s"
		}
		announcement += " found"
		fmt.Fprintf(w, `<div id="search-announcer" class="sr-only" aria-live="polite" aria-atomic="true" hx-swap-oob="true">%s</div>`, html.EscapeString(announcement))
		if count == 0 {
			fmt.Fprint(w, "<p>No notes with this tag.</p>")
			return
		}
		if isCmdPalette {
			for i, n := range results {
				itemURL := "/notes/view?title=" + url.QueryEscape(n.Title)
				fmt.Fprintf(w,
					`<li role="option" id="cmd-result-%d" aria-selected="false"><a href="%s">#%s &mdash; %s</a></li>`,
					i, html.EscapeString(itemURL), html.EscapeString(tag), html.EscapeString(n.Title),
				)
			}
		} else {
			type searchItem struct {
				Title string
				URL   string
			}
			items := make([]searchItem, 0, count)
			for _, n := range results {
				items = append(items, searchItem{
					Title: n.Title,
					URL:   "/notes/view?title=" + url.QueryEscape(n.Title),
				})
			}
			tmpl := template.Must(template.New("tagsearch").Parse(`<ul role="list">{{range .}}<li><a href="{{.URL}}">{{.Title}}</a></li>{{end}}</ul>`))
			tmpl.Execute(w, items)
		}
		return
	}

	results, err := s.DBManager.SearchNotes(db, q)
	if err != nil {
		log.Printf("SearchHandler: SearchNotes: %v", err)
		http.Error(w, s.t(r, "error.internal"), http.StatusInternalServerError)
		return
	}

	count := len(results)
	announcement := fmt.Sprintf("%d result", count)
	if count != 1 {
		announcement += "s"
	}
	announcement += " found"

	fmt.Fprintf(w, `<div id="search-announcer" class="sr-only" aria-live="polite" aria-atomic="true" hx-swap-oob="true">%s</div>`, html.EscapeString(announcement))

	if count == 0 {
		fmt.Fprint(w, "<p>No matches found.</p>")
		return
	}

	type searchItem struct {
		Title   string
		URL     string
		Snippet template.HTML
	}
	items := make([]searchItem, 0, count)
	for _, res := range results {
		items = append(items, searchItem{
			Title:   res.Title,
			URL:     "/notes/view?title=" + url.QueryEscape(res.Title),
			Snippet: template.HTML(res.Snippet),
		})
	}

	if isCmdPalette {
		for i, item := range items {
			fmt.Fprintf(w,
				`<li role="option" id="cmd-result-%d" aria-selected="false"><a href="%s">%s</a></li>`,
				i, html.EscapeString(item.URL), html.EscapeString(item.Title),
			)
		}
		return
	}

	tmpl := template.Must(template.New("search").Parse(`<ul role="list">{{range .}}<li><a href="{{.URL}}">{{.Title}}</a>{{if .Snippet}}<br><small style="color:#666">{{.Snippet}}</small>{{end}}</li>{{end}}</ul>`))
	tmpl.Execute(w, items)
}

// TagsHandler serves GET /tags/{tag} — lists all notes with that tag.
func (s *Server) TagsHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUserID(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	tag := strings.ToLower(strings.TrimPrefix(r.URL.Path, "/tags/"))
	tag = strings.TrimSuffix(tag, "/")

	db, err := s.DBManager.OpenUserDB(userID)
	if err != nil {
		log.Printf("TagsHandler: OpenUserDB: %v", err)
		http.Error(w, s.t(r, "error.db_unavailable"), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	notes, err := s.DBManager.GetNotesByTag(db, tag)
	if err != nil {
		log.Printf("TagsHandler: GetNotesByTag: %v", err)
		http.Error(w, s.t(r, "error.internal"), http.StatusInternalServerError)
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
			log.Printf("TagsHandler: RenderMarkdownWithWikiLinks: %v", err)
			http.Error(w, s.t(r, "error.internal"), http.StatusInternalServerError)
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

	notebooks, err := s.DBManager.ListNotebooks(db)
	if err != nil {
		notebooks = []database.Notebook{}
	}
	inboxCount, _ := s.DBManager.CountInboxNotes(db)
	allTags, _ := s.DBManager.ListAllTags(db)

	s.RenderTemplate(w, r, "notes.gohtml", map[string]interface{}{
		"Notes":      rendered,
		"Notebooks":  notebooks,
		"InboxCount": inboxCount,
		"AllTags":    allTags,
		"IsTagView":  true,
		"ActiveTag":  tag,
	})
}
