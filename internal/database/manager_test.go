package database

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	manager := &DatabaseManager{}
	if err := manager.ensureUserSchema(db); err != nil {
		t.Fatalf("Failed to setup schema: %v", err)
	}

	return db
}

func setupSystemTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open system test database: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY,
		email TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`)
	if err != nil {
		t.Fatalf("Failed to create users table: %v", err)
	}
	return db
}

func TestCreateSystemUser(t *testing.T) {
	db := setupSystemTestDB(t)
	defer db.Close()

	manager := &DatabaseManager{}
	user := User{ID: "user-1", Email: "alice@example.com", PasswordHash: "hash1"}

	if err := manager.CreateSystemUser(db, user); err != nil {
		t.Fatalf("CreateSystemUser failed: %v", err)
	}

	got, err := manager.GetUserByEmail(db, user.Email)
	if err != nil {
		t.Fatalf("GetUserByEmail failed: %v", err)
	}
	if got == nil {
		t.Fatal("Expected user, got nil")
	}
	if got.ID != user.ID || got.Email != user.Email || got.PasswordHash != user.PasswordHash {
		t.Errorf("User mismatch: got %+v, want %+v", got, user)
	}
}

func TestGetUserByEmail_NotFound(t *testing.T) {
	db := setupSystemTestDB(t)
	defer db.Close()

	manager := &DatabaseManager{}

	got, err := manager.GetUserByEmail(db, "nobody@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail failed: %v", err)
	}
	if got != nil {
		t.Errorf("Expected nil for unknown email, got %+v", got)
	}
}

func TestUpdateUserPassword(t *testing.T) {
	db := setupSystemTestDB(t)
	defer db.Close()

	manager := &DatabaseManager{}
	user := User{ID: "user-2", Email: "bob@example.com", PasswordHash: "old-hash"}

	if err := manager.CreateSystemUser(db, user); err != nil {
		t.Fatalf("CreateSystemUser failed: %v", err)
	}

	newHash := "new-hash"
	if err := manager.UpdateUserPassword(db, user.ID, newHash); err != nil {
		t.Fatalf("UpdateUserPassword failed: %v", err)
	}

	got, err := manager.GetUserByEmail(db, user.Email)
	if err != nil {
		t.Fatalf("GetUserByEmail after update failed: %v", err)
	}
	if got == nil {
		t.Fatal("Expected user after update, got nil")
	}
	if got.PasswordHash != newHash {
		t.Errorf("PasswordHash = %q, want %q", got.PasswordHash, newHash)
	}
}

func TestUpdateUserPassword_UnknownUser(t *testing.T) {
	db := setupSystemTestDB(t)
	defer db.Close()

	manager := &DatabaseManager{}

	// Should not error even when no row is matched
	if err := manager.UpdateUserPassword(db, "ghost-id", "some-hash"); err != nil {
		t.Fatalf("UpdateUserPassword returned unexpected error for unknown user: %v", err)
	}
}

func TestCreateNoteAndGetNoteByID(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	manager := &DatabaseManager{}

	note := Note{
		ID:         "test-id",
		Title:      "Test Note",
		Content:    "This is test content.",
		NotebookID: "notebook-1",
	}

	err := manager.CreateNote(db, note)
	if err != nil {
		t.Fatalf("CreateNote failed: %v", err)
	}

	retrieved, err := manager.GetNoteByID(db, "test-id")
	if err != nil {
		t.Fatalf("GetNoteByID failed: %v", err)
	}

	if retrieved == nil {
		t.Fatal("Note not found")
	}

	if retrieved.ID != note.ID || retrieved.Title != note.Title || retrieved.Content != note.Content || retrieved.NotebookID != note.NotebookID {
		t.Errorf("Retrieved note does not match: got %+v, want %+v", retrieved, note)
	}
}

func TestInsertNoteTags(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	manager := &DatabaseManager{}

	note := Note{
		ID:    "test-id",
		Title: "Test Note",
	}

	err := manager.CreateNote(db, note)
	if err != nil {
		t.Fatalf("CreateNote failed: %v", err)
	}

	initialTags := []string{"tag1", "tag2"}
	err = manager.InsertNoteTags(db, "test-id", initialTags)
	if err != nil {
		t.Fatalf("InsertNoteTags failed: %v", err)
	}

	rows, err := db.Query("SELECT tag FROM note_tags WHERE note_id = ? ORDER BY tag", "test-id")
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	defer rows.Close()

	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			t.Fatalf("Scan failed: %v", err)
		}
		tags = append(tags, tag)
	}

	expected := []string{"tag1", "tag2"}
	if !reflect.DeepEqual(tags, expected) {
		t.Errorf("Initial tags = %v, want %v", tags, expected)
	}

	newTags := []string{"tag3", "tag4"}
	err = manager.InsertNoteTags(db, "test-id", newTags)
	if err != nil {
		t.Fatalf("InsertNoteTags update failed: %v", err)
	}

	rows, err = db.Query("SELECT tag FROM note_tags WHERE note_id = ? ORDER BY tag", "test-id")
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	defer rows.Close()

	tags = []string{}
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			t.Fatalf("Scan failed: %v", err)
		}
		tags = append(tags, tag)
	}

	expected = []string{"tag3", "tag4"}
	if !reflect.DeepEqual(tags, expected) {
		t.Errorf("Updated tags = %v, want %v", tags, expected)
	}
}

func TestSearchNotes(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	manager := &DatabaseManager{}

	notes := []Note{
		{ID: "1", Title: "Go Programming", Content: "Learning Go language."},
		{ID: "2", Title: "Python Basics", Content: "Introduction to Python."},
		{ID: "3", Title: "Database Design", Content: "Designing databases."},
	}

	for _, note := range notes {
		err := manager.CreateNote(db, note)
		if err != nil {
			t.Fatalf("CreateNote failed: %v", err)
		}
	}

	results, err := manager.SearchNotes(db, "Go")
	if err != nil {
		t.Fatalf("SearchNotes failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}

	if results[0].Title != "Go Programming" {
		t.Errorf("Expected title 'Go Programming', got '%s'", results[0].Title)
	}

	results, err = manager.SearchNotes(db, "Python")
	if err != nil {
		t.Fatalf("SearchNotes failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}

	if results[0].Title != "Python Basics" {
		t.Errorf("Expected title 'Python Basics', got '%s'", results[0].Title)
	}
}

func TestSearchNotes_Triggers_UpdateDelete(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	manager := &DatabaseManager{}

	note := Note{ID: "1", Title: "Sync Test", Content: "first content"}
	if err := manager.CreateNote(db, note); err != nil {
		t.Fatalf("CreateNote failed: %v", err)
	}

	results, err := manager.SearchNotes(db, "first")
	if err != nil {
		t.Fatalf("SearchNotes failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	note.Content = "updated content"
	if err := manager.UpdateNote(db, note); err != nil {
		t.Fatalf("UpdateNote failed: %v", err)
	}

	results, err = manager.SearchNotes(db, "updated")
	if err != nil {
		t.Fatalf("SearchNotes failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result after update, got %d", len(results))
	}

	results, err = manager.SearchNotes(db, "first")
	if err != nil {
		t.Fatalf("SearchNotes failed: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("Expected 0 results for old content, got %d", len(results))
	}

	if err := manager.DeleteNote(db, "1"); err != nil {
		t.Fatalf("DeleteNote failed: %v", err)
	}

	results, err = manager.SearchNotes(db, "updated")
	if err != nil {
		t.Fatalf("SearchNotes failed: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("Expected 0 results after delete, got %d", len(results))
	}
}

func TestSearchNotes_EdgeCases(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	manager := &DatabaseManager{}

	if results, err := manager.SearchNotes(db, ""); err != nil {
		t.Fatalf("SearchNotes failed: %v", err)
	} else if len(results) != 0 {
		t.Fatalf("Expected 0 results for empty query, got %d", len(results))
	}

	note := Note{ID: "1", Title: "Special Characters", Content: "C++ and Go!"}
	if err := manager.CreateNote(db, note); err != nil {
		t.Fatalf("CreateNote failed: %v", err)
	}

	if results, err := manager.SearchNotes(db, "C++"); err != nil {
		t.Fatalf("SearchNotes failed: %v", err)
	} else if len(results) != 1 {
		t.Fatalf("Expected 1 result for special characters, got %d", len(results))
	}

	if results, err := manager.SearchNotes(db, "Go Python"); err != nil {
		t.Fatalf("SearchNotes failed: %v", err)
	} else if len(results) != 0 {
		t.Fatalf("Expected 0 results for multiple keywords that don't match, got %d", len(results))
	}
}

func TestDeleteNoteCleansUpTags(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	manager := &DatabaseManager{}

	note := Note{ID: "test-id", Title: "Test Note"}
	if err := manager.CreateNote(db, note); err != nil {
		t.Fatalf("CreateNote failed: %v", err)
	}

	if err := manager.InsertNoteTags(db, "test-id", []string{"tag1", "tag2"}); err != nil {
		t.Fatalf("InsertNoteTags failed: %v", err)
	}

	if err := manager.DeleteNote(db, "test-id"); err != nil {
		t.Fatalf("DeleteNote failed: %v", err)
	}

	rows, err := db.Query("SELECT tag FROM note_tags WHERE note_id = ?", "test-id")
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	defer rows.Close()

	if rows.Next() {
		t.Fatalf("Expected no tags after note deletion, but found some")
	}
}

func TestGetNotesByNotebookID_IsolationAndIndex(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	manager := &DatabaseManager{}

	note1 := Note{ID: "n1", Title: "Note 1", Content: "A", NotebookID: "nb1"}
	note2 := Note{ID: "n2", Title: "Note 2", Content: "B", NotebookID: "nb2"}
	if err := manager.CreateNote(db, note1); err != nil {
		t.Fatalf("CreateNote failed: %v", err)
	}
	if err := manager.CreateNote(db, note2); err != nil {
		t.Fatalf("CreateNote failed: %v", err)
	}

	notes, err := manager.GetNotesByNotebookID(db, "nb1")
	if err != nil {
		t.Fatalf("GetNotesByNotebookID failed: %v", err)
	}
	if len(notes) != 1 || notes[0].ID != "n1" {
		t.Fatalf("Expected only note n1 for notebook nb1, got %v", notes)
	}

	rows, err := db.Query("EXPLAIN QUERY PLAN SELECT id, title, content, updated_at, notebook_id FROM notes WHERE notebook_id = ? ORDER BY updated_at DESC;", "nb1")
	if err != nil {
		t.Fatalf("Explain query plan failed: %v", err)
	}
	defer rows.Close()

	foundIndex := false
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("Scan failed: %v", err)
		}
		if strings.Contains(detail, "idx_notes_notebook_id") {
			foundIndex = true
		}
	}

	if !foundIndex {
		t.Fatalf("Expected query plan to use idx_notes_notebook_id")
	}
}

func TestGetTagsByNoteID(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	manager := &DatabaseManager{}

	if err := manager.CreateNote(db, Note{ID: "note-a", Title: "A"}); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if err := manager.InsertNoteTags(db, "note-a", []string{"go", "testing", "database"}); err != nil {
		t.Fatalf("InsertNoteTags: %v", err)
	}

	tags, err := manager.GetTagsByNoteID(db, "note-a")
	if err != nil {
		t.Fatalf("GetTagsByNoteID: %v", err)
	}
	// Results must be sorted alphabetically
	expected := []string{"database", "go", "testing"}
	if !reflect.DeepEqual(tags, expected) {
		t.Fatalf("Expected %v, got %v", expected, tags)
	}

	// Note with no tags returns nil slice, no error
	if err := manager.CreateNote(db, Note{ID: "note-b", Title: "B"}); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	empty, err := manager.GetTagsByNoteID(db, "note-b")
	if err != nil {
		t.Fatalf("GetTagsByNoteID (no tags): %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("Expected empty slice for note with no tags, got %v", empty)
	}
}

func TestListAllTags(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	manager := &DatabaseManager{}

	// Empty DB returns empty, no error
	tags, err := manager.ListAllTags(db)
	if err != nil {
		t.Fatalf("ListAllTags on empty db: %v", err)
	}
	if len(tags) != 0 {
		t.Fatalf("Expected no tags, got %v", tags)
	}

	if err := manager.CreateNote(db, Note{ID: "n1", Title: "N1"}); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if err := manager.CreateNote(db, Note{ID: "n2", Title: "N2"}); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	// Same tag on two notes must appear only once (DISTINCT)
	if err := manager.InsertNoteTags(db, "n1", []string{"alpha", "beta"}); err != nil {
		t.Fatalf("InsertNoteTags: %v", err)
	}
	if err := manager.InsertNoteTags(db, "n2", []string{"beta", "gamma"}); err != nil {
		t.Fatalf("InsertNoteTags: %v", err)
	}

	tags, err = manager.ListAllTags(db)
	if err != nil {
		t.Fatalf("ListAllTags: %v", err)
	}
	expected := []string{"alpha", "beta", "gamma"}
	if !reflect.DeepEqual(tags, expected) {
		t.Fatalf("Expected %v, got %v", expected, tags)
	}
}

func TestGetNotesByTag(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	manager := &DatabaseManager{}

	if err := manager.CreateNote(db, Note{ID: "x1", Title: "X1", NotebookID: "nb1"}); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if err := manager.CreateNote(db, Note{ID: "x2", Title: "X2", NotebookID: ""}); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if err := manager.CreateNote(db, Note{ID: "x3", Title: "X3", NotebookID: "nb1"}); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if err := manager.InsertNoteTags(db, "x1", []string{"mytag"}); err != nil {
		t.Fatalf("InsertNoteTags: %v", err)
	}
	if err := manager.InsertNoteTags(db, "x3", []string{"mytag", "other"}); err != nil {
		t.Fatalf("InsertNoteTags: %v", err)
	}

	notes, err := manager.GetNotesByTag(db, "mytag")
	if err != nil {
		t.Fatalf("GetNotesByTag: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("Expected 2 notes with tag 'mytag', got %d: %v", len(notes), notes)
	}

	// Verify note x2 (untagged) is not included
	for _, n := range notes {
		if n.ID == "x2" {
			t.Fatal("Note x2 should not appear in tag results")
		}
	}

	// Unknown tag returns empty slice, no error
	none, err := manager.GetNotesByTag(db, "nonexistent")
	if err != nil {
		t.Fatalf("GetNotesByTag (unknown tag): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("Expected no notes for unknown tag, got %v", none)
	}
}

func TestListInboxNotes(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	manager := &DatabaseManager{}

	// Notes without a notebook_id land in the inbox.
	if err := manager.CreateNote(db, Note{ID: "i1", Title: "Inbox 1"}); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if err := manager.CreateNote(db, Note{ID: "i2", Title: "Inbox 2", NotebookID: ""}); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	// Note assigned to a notebook must NOT appear in the inbox.
	if err := manager.CreateNote(db, Note{ID: "n1", Title: "Notebook Note", NotebookID: "nb-1"}); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	notes, err := manager.ListInboxNotes(db)
	if err != nil {
		t.Fatalf("ListInboxNotes: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("Expected 2 inbox notes, got %d: %v", len(notes), notes)
	}
	for _, n := range notes {
		if n.NotebookID != "" {
			t.Errorf("Inbox note %q has NotebookID %q, expected empty", n.ID, n.NotebookID)
		}
	}

	// Notebook note must not appear.
	for _, n := range notes {
		if n.ID == "n1" {
			t.Fatal("Notebook-assigned note should not appear in inbox")
		}
	}
}

func TestCountInboxNotes(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	manager := &DatabaseManager{}

	// Empty database: count must be 0.
	count, err := manager.CountInboxNotes(db)
	if err != nil {
		t.Fatalf("CountInboxNotes on empty db: %v", err)
	}
	if count != 0 {
		t.Fatalf("Expected 0 inbox notes, got %d", count)
	}

	// One inbox note (no notebook_id).
	if err := manager.CreateNote(db, Note{ID: "c1", Title: "Capture"}); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	count, err = manager.CountInboxNotes(db)
	if err != nil {
		t.Fatalf("CountInboxNotes: %v", err)
	}
	if count != 1 {
		t.Fatalf("Expected 1 inbox note, got %d", count)
	}

	// Assigning c1 to a notebook must reduce count to 0.
	if err := manager.UpdateNote(db, Note{ID: "c1", Title: "Capture", NotebookID: "nb-x"}); err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	count, err = manager.CountInboxNotes(db)
	if err != nil {
		t.Fatalf("CountInboxNotes after update: %v", err)
	}
	if count != 0 {
		t.Fatalf("Expected 0 inbox notes after assignment, got %d", count)
	}
}

func TestGetNotebookIndexNote(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	manager := &DatabaseManager{}

	notebookID := "toc-nb"

	// No notes yet — must return nil without error.
	idx, err := manager.GetNotebookIndexNote(db, notebookID)
	if err != nil {
		t.Fatalf("GetNotebookIndexNote (empty): %v", err)
	}
	if idx != nil {
		t.Fatalf("Expected nil when no index note exists, got %+v", idx)
	}

	// Create two notes in the notebook — neither tagged #index yet.
	if err := manager.CreateNote(db, Note{ID: "p1", Title: "Plain 1", NotebookID: notebookID}); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if err := manager.CreateNote(db, Note{ID: "p2", Title: "Plain 2", NotebookID: notebookID}); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	// Still no index note.
	idx, err = manager.GetNotebookIndexNote(db, notebookID)
	if err != nil {
		t.Fatalf("GetNotebookIndexNote (no index tag): %v", err)
	}
	if idx != nil {
		t.Fatalf("Expected nil when no note has #index tag, got %+v", idx)
	}

	// Tag p1 with #index — it becomes the index note for this notebook.
	if err := manager.InsertNoteTags(db, "p1", []string{"index"}); err != nil {
		t.Fatalf("InsertNoteTags: %v", err)
	}
	idx, err = manager.GetNotebookIndexNote(db, notebookID)
	if err != nil {
		t.Fatalf("GetNotebookIndexNote: %v", err)
	}
	if idx == nil {
		t.Fatal("Expected index note, got nil")
	}
	if idx.ID != "p1" {
		t.Errorf("Expected index note ID p1, got %q", idx.ID)
	}

	// A note from a DIFFERENT notebook tagged #index must not be returned.
	if err := manager.CreateNote(db, Note{ID: "other", Title: "Other NB Index", NotebookID: "other-nb"}); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if err := manager.InsertNoteTags(db, "other", []string{"index"}); err != nil {
		t.Fatalf("InsertNoteTags: %v", err)
	}
	idx, err = manager.GetNotebookIndexNote(db, notebookID)
	if err != nil {
		t.Fatalf("GetNotebookIndexNote (isolation): %v", err)
	}
	if idx == nil || idx.ID != "p1" {
		t.Errorf("Expected index note p1 after adding index note for different notebook, got %v", idx)
	}
}

func TestBackfillTutorialShowcase_SeedsMissingAndSkipsExisting(t *testing.T) {
	base := t.TempDir()
	mgr := NewManager(base)

	systemDB, err := mgr.InitSystemDB()
	if err != nil {
		t.Fatalf("InitSystemDB: %v", err)
	}
	defer systemDB.Close()

	userWithTutorial := User{ID: "u-has", Email: "has@example.com", PasswordHash: "x"}
	userWithoutTutorial := User{ID: "u-miss", Email: "miss@example.com", PasswordHash: "y"}

	if err := mgr.CreateSystemUser(systemDB, userWithTutorial); err != nil {
		t.Fatalf("CreateSystemUser userWithTutorial: %v", err)
	}
	if err := mgr.CreateSystemUser(systemDB, userWithoutTutorial); err != nil {
		t.Fatalf("CreateSystemUser userWithoutTutorial: %v", err)
	}

	if _, err := mgr.CreateUserDB(userWithTutorial.ID); err != nil {
		t.Fatalf("CreateUserDB userWithTutorial: %v", err)
	}
	if _, err := mgr.CreateUserDB(userWithoutTutorial.ID); err != nil {
		t.Fatalf("CreateUserDB userWithoutTutorial: %v", err)
	}

	// Mark userWithTutorial as already having tutorial content.
	dbHas, err := mgr.OpenUserDB(userWithTutorial.ID)
	if err != nil {
		t.Fatalf("OpenUserDB userWithTutorial: %v", err)
	}
	if err := mgr.CreateNote(dbHas, Note{ID: "n1", Title: "Legacy Tutorial", Content: "#tutorial"}); err != nil {
		dbHas.Close()
		t.Fatalf("CreateNote legacy tutorial: %v", err)
	}
	if err := mgr.InsertNoteTags(dbHas, "n1", []string{"tutorial"}); err != nil {
		dbHas.Close()
		t.Fatalf("InsertNoteTags legacy tutorial: %v", err)
	}
	dbHas.Close()

	translations := map[string]string{
		"tutorial.notebook.name":       "Notty Showcase",
		"tutorial.note1.title":         "Start Here: Markdown 80/20 Playground",
		"tutorial.note2.title":         "Connect Ideas with Wiki Links",
		"tutorial.note3.title":         "Find Anything with Search and Tags",
		"tutorial.markdown.formatting": "# Start Here: Markdown 80/20 Playground\n#tutorial",
		"tutorial.markdown.advanced":   "# Connect Ideas with Wiki Links\n#tutorial",
		"tutorial.markdown.wikilinks":  "# Find Anything with Search and Tags\n#tutorial",
	}

	seeded, skipped, err := mgr.BackfillTutorialShowcase(systemDB, translations)
	if err != nil {
		t.Fatalf("BackfillTutorialShowcase first run: %v", err)
	}
	if seeded != 1 || skipped != 1 {
		t.Fatalf("first run counts: seeded=%d skipped=%d, want seeded=1 skipped=1", seeded, skipped)
	}

	// User without tutorial should now have tutorial-tagged content.
	dbMiss, err := mgr.OpenUserDB(userWithoutTutorial.ID)
	if err != nil {
		t.Fatalf("OpenUserDB userWithoutTutorial: %v", err)
	}
	hasTutorial, err := mgr.hasTutorialNotes(dbMiss)
	if err != nil {
		dbMiss.Close()
		t.Fatalf("hasTutorialNotes userWithoutTutorial: %v", err)
	}
	if !hasTutorial {
		dbMiss.Close()
		t.Fatal("expected tutorial notes for userWithoutTutorial after backfill")
	}
	dbMiss.Close()

	// Second run should be idempotent because seed markers are present.
	seeded2, skipped2, err := mgr.BackfillTutorialShowcase(systemDB, translations)
	if err != nil {
		t.Fatalf("BackfillTutorialShowcase second run: %v", err)
	}
	if seeded2 != 0 || skipped2 != 2 {
		t.Fatalf("second run counts: seeded=%d skipped=%d, want seeded=0 skipped=2", seeded2, skipped2)
	}
}
