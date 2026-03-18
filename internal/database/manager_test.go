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
