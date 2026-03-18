package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

type DatabaseManager struct {
	SystemDBPath string
	UserDBDir    string
}

type User struct {
	ID           string
	Email        string
	PasswordHash string
}

type Note struct {
	ID         string
	Title      string
	Content    string
	UpdatedAt  string
	NotebookID string
}

type Notebook struct {
	ID        string
	Name      string
	CreatedAt string
}

func NewManager(basePath string) *DatabaseManager {
	return &DatabaseManager{
		SystemDBPath: filepath.Join(basePath, "system.db"),
		UserDBDir:    filepath.Join(basePath, "users"),
	}
}

func (m *DatabaseManager) ensureStoragePath(basePath string) error {
	return os.MkdirAll(basePath, 0o755)
}

func (m *DatabaseManager) InitSystemDB() (*sql.DB, error) {
	systemDir := filepath.Dir(m.SystemDBPath)
	if err := m.ensureStoragePath(systemDir); err != nil {
		return nil, fmt.Errorf("unable to create system storage path: %w", err)
	}

	db, err := sql.Open("sqlite", m.SystemDBPath)
	if err != nil {
		return nil, fmt.Errorf("unable to open system database: %w", err)
	}

	createUsersTable := `CREATE TABLE IF NOT EXISTS users (
        id TEXT PRIMARY KEY,
        email TEXT NOT NULL UNIQUE,
        password_hash TEXT NOT NULL,
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP
    );`

	if _, err := db.Exec(createUsersTable); err != nil {
		db.Close()
		return nil, fmt.Errorf("unable to create users table: %w", err)
	}

	return db, nil
}

func (m *DatabaseManager) createUserDBFolder() error {
	return m.ensureStoragePath(m.UserDBDir)
}

func (m *DatabaseManager) CreateUserDB(userID string) (string, error) {
	if err := m.createUserDBFolder(); err != nil {
		return "", fmt.Errorf("unable to create user db folder: %w", err)
	}

	userDBPath := filepath.Join(m.UserDBDir, fmt.Sprintf("%s.db", userID))
	db, err := sql.Open("sqlite", userDBPath)
	if err != nil {
		return "", fmt.Errorf("unable to open user database: %w", err)
	}
	defer db.Close()

	if err := m.ensureUserSchema(db); err != nil {
		db.Close()
		return "", err
	}

	return userDBPath, nil
}

func (m *DatabaseManager) CreateNote(db *sql.DB, note Note) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("unable to begin create note transaction: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	insert := `INSERT INTO notes (id, title, content, notebook_id) VALUES (?, ?, ?, ?);`
	if _, err = tx.Exec(insert, note.ID, note.Title, note.Content, note.NotebookID); err != nil {
		return fmt.Errorf("unable to create note: %w", err)
	}
	if _, err = tx.Exec(`INSERT INTO notes_fts (id, title, content) VALUES (?, ?, ?);`, note.ID, note.Title, note.Content); err != nil {
		return fmt.Errorf("unable to index note in FTS: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("unable to commit create note: %w", err)
	}
	return nil
}

func (m *DatabaseManager) ListNotes(db *sql.DB) ([]Note, error) {
	query := `SELECT id, title, content, updated_at, COALESCE(notebook_id, '') FROM notes ORDER BY updated_at DESC LIMIT 100;`
	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("unable to list notes: %w", err)
	}
	defer rows.Close()

	notes := []Note{}
	for rows.Next() {
		n := Note{}
		if err := rows.Scan(&n.ID, &n.Title, &n.Content, &n.UpdatedAt, &n.NotebookID); err != nil {
			return nil, fmt.Errorf("unable to scan note: %w", err)
		}
		notes = append(notes, n)
	}
	return notes, nil
}

func (m *DatabaseManager) GetNoteByID(db *sql.DB, noteID string) (*Note, error) {
	query := `SELECT id, title, content, updated_at, COALESCE(notebook_id, '') FROM notes WHERE id = ?;`
	note := &Note{}
	row := db.QueryRow(query, noteID)
	if err := row.Scan(&note.ID, &note.Title, &note.Content, &note.UpdatedAt, &note.NotebookID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("unable to get note: %w", err)
	}
	return note, nil
}

func (m *DatabaseManager) GetNoteByTitle(db *sql.DB, title string) (*Note, error) {
	query := `SELECT id, title, content, updated_at, COALESCE(notebook_id, '') FROM notes WHERE lower(title) = lower(?) LIMIT 1;`
	note := &Note{}
	row := db.QueryRow(query, title)
	if err := row.Scan(&note.ID, &note.Title, &note.Content, &note.UpdatedAt, &note.NotebookID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("unable to get note by title: %w", err)
	}
	return note, nil
}

func (m *DatabaseManager) SearchNotes(db *sql.DB, queryText string) ([]Note, error) {
	if strings.TrimSpace(queryText) == "" {
		return []Note{}, nil
	}
	ftsQuery := fmt.Sprintf("%s*", queryText)
	rows, err := db.Query(`SELECT id, title, content, updated_at FROM notes_fts WHERE notes_fts MATCH ? LIMIT 50;`, ftsQuery)
	if err != nil {
		return nil, fmt.Errorf("unable to search notes: %w", err)
	}
	defer rows.Close()

	notes := []Note{}
	for rows.Next() {
		note := Note{}
		if err := rows.Scan(&note.ID, &note.Title, &note.Content, &note.UpdatedAt); err != nil {
			return nil, fmt.Errorf("unable to scan search note: %w", err)
		}
		notes = append(notes, note)
	}
	return notes, nil
}

func (m *DatabaseManager) UpdateNote(db *sql.DB, note Note) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("unable to begin update transaction: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	update := `UPDATE notes SET title = ?, content = ?, notebook_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;`
	if _, err = tx.Exec(update, note.Title, note.Content, note.NotebookID, note.ID); err != nil {
		return fmt.Errorf("unable to update note: %w", err)
	}
	if _, err = tx.Exec(`DELETE FROM notes_fts WHERE id = ?;`, note.ID); err != nil {
		return fmt.Errorf("unable to delete old fts row: %w", err)
	}
	if _, err = tx.Exec(`INSERT INTO notes_fts (id, title, content) VALUES (?, ?, ?);`, note.ID, note.Title, note.Content); err != nil {
		return fmt.Errorf("unable to update fts row: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("unable to commit update note: %w", err)
	}
	return nil
}

func (m *DatabaseManager) DeleteNote(db *sql.DB, noteID string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("unable to begin delete transaction: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	if _, err = tx.Exec(`DELETE FROM notes WHERE id = ?;`, noteID); err != nil {
		return fmt.Errorf("unable to delete note: %w", err)
	}
	if _, err = tx.Exec(`DELETE FROM notes_fts WHERE id = ?;`, noteID); err != nil {
		return fmt.Errorf("unable to delete fts note: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("unable to commit delete note: %w", err)
	}
	return nil
}

func (m *DatabaseManager) InsertNoteLinks(db *sql.DB, sourceID string, targetIDs []string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("unable to begin insert links transaction: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	for _, targetID := range targetIDs {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO note_links (source_id, target_id) VALUES (?, ?);`, sourceID, targetID); err != nil {
			return fmt.Errorf("unable to insert note link: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("unable to commit insert links: %w", err)
	}
	return nil
}

func (m *DatabaseManager) InsertNoteTags(db *sql.DB, noteID string, tags []string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("unable to begin insert tags transaction: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	if _, err = tx.Exec(`DELETE FROM note_tags WHERE note_id = ?;`, noteID); err != nil {
		return fmt.Errorf("unable to delete existing tags: %w", err)
	}
	for _, tag := range tags {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO note_tags (note_id, tag) VALUES (?, ?);`, noteID, tag); err != nil {
			return fmt.Errorf("unable to insert note tag: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("unable to commit insert tags: %w", err)
	}
	return nil
}

func (m *DatabaseManager) DeleteNoteLinks(db *sql.DB, sourceID string) error {
	_, err := db.Exec(`DELETE FROM note_links WHERE source_id = ?;`, sourceID)
	if err != nil {
		return fmt.Errorf("unable to delete note links: %w", err)
	}
	return nil
}

func (m *DatabaseManager) GetBacklinks(db *sql.DB, targetID string) ([]Note, error) {
	query := `SELECT n.id, n.title, n.content, n.updated_at, COALESCE(n.notebook_id, '') FROM notes n JOIN note_links l ON n.id = l.source_id WHERE l.target_id = ? ORDER BY n.updated_at DESC;`
	rows, err := db.Query(query, targetID)
	if err != nil {
		return nil, fmt.Errorf("unable to query backlinks: %w", err)
	}
	defer rows.Close()

	notes := []Note{}
	for rows.Next() {
		n := Note{}
		if err := rows.Scan(&n.ID, &n.Title, &n.Content, &n.UpdatedAt, &n.NotebookID); err != nil {
			return nil, fmt.Errorf("unable to scan backlink: %w", err)
		}
		notes = append(notes, n)
	}
	return notes, nil
}

func (m *DatabaseManager) ensureUserSchema(db *sql.DB) error {
	createNotesTable := `CREATE TABLE IF NOT EXISTS notes (
        id TEXT PRIMARY KEY,
        title TEXT NOT NULL COLLATE NOCASE,
        content TEXT NOT NULL,
        updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
    );`
	if _, err := db.Exec(createNotesTable); err != nil {
		return fmt.Errorf("unable to ensure notes table: %w", err)
	}

	createFTSTable := `CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(id UNINDEXED, title, content);`
	if _, err := db.Exec(createFTSTable); err != nil {
		return fmt.Errorf("unable to ensure notes_fts table: %w", err)
	}

	createLinksTable := `CREATE TABLE IF NOT EXISTS note_links (source_id TEXT, target_id TEXT, UNIQUE(source_id, target_id));`
	if _, err := db.Exec(createLinksTable); err != nil {
		return fmt.Errorf("unable to ensure note_links table: %w", err)
	}

	createNotebooksTable := `CREATE TABLE IF NOT EXISTS notebooks (id TEXT PRIMARY KEY, name TEXT NOT NULL COLLATE NOCASE, created_at DATETIME DEFAULT CURRENT_TIMESTAMP);`
	if _, err := db.Exec(createNotebooksTable); err != nil {
		return fmt.Errorf("unable to ensure notebooks table: %w", err)
	}

	createNoteTagsTable := `CREATE TABLE IF NOT EXISTS note_tags (note_id TEXT, tag TEXT, UNIQUE(note_id, tag));`
	if _, err := db.Exec(createNoteTagsTable); err != nil {
		return fmt.Errorf("unable to ensure note_tags table: %w", err)
	}

	alterNotesTable := `PRAGMA table_info(notes);`
	rows, err := db.Query(alterNotesTable)
	if err == nil {
		defer rows.Close()
		hasNotebookID := false
		for rows.Next() {
			var cid int
			var name string
			var typeVal string
			var notnull int
			var dfltValue interface{}
			var pk int
			if err := rows.Scan(&cid, &name, &typeVal, &notnull, &dfltValue, &pk); err == nil && name == "notebook_id" {
				hasNotebookID = true
			}
		}
		if !hasNotebookID {
			if _, err := db.Exec(`ALTER TABLE notes ADD COLUMN notebook_id TEXT;`); err != nil {
				return fmt.Errorf("unable to alter notes table: %w", err)
			}
		}
	}

	indexSQL := `CREATE INDEX IF NOT EXISTS idx_notes_notebook_id ON notes(notebook_id);`
	if _, err := db.Exec(indexSQL); err != nil {
		return fmt.Errorf("unable to create notebook_id index: %w", err)
	}

	return nil
}

func (m *DatabaseManager) OpenUserDB(userID string) (*sql.DB, error) {
	userDBPath := filepath.Join(m.UserDBDir, fmt.Sprintf("%s.db", userID))
	db, err := sql.Open("sqlite", userDBPath)
	if err != nil {
		return nil, fmt.Errorf("unable to open user DB for %s: %w", userID, err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := m.ensureUserSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func (m *DatabaseManager) CreateSystemUser(db *sql.DB, user User) error {
	insert := `INSERT INTO users (id, email, password_hash) VALUES (?, ?, ?);`
	_, err := db.Exec(insert, user.ID, user.Email, user.PasswordHash)
	if err != nil {
		return fmt.Errorf("unable to insert user: %w", err)
	}
	return nil
}

func (m *DatabaseManager) GetUserByEmail(db *sql.DB, email string) (*User, error) {
	query := `SELECT id, email, password_hash FROM users WHERE email = ?;`
	row := db.QueryRow(query, email)
	user := &User{}
	if err := row.Scan(&user.ID, &user.Email, &user.PasswordHash); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("unable to query user by email: %w", err)
	}
	return user, nil
}

func (m *DatabaseManager) CreateNotebook(db *sql.DB, notebook Notebook) error {
	insert := `INSERT INTO notebooks (id, name) VALUES (?, ?);`
	_, err := db.Exec(insert, notebook.ID, notebook.Name)
	if err != nil {
		return fmt.Errorf("unable to create notebook: %w", err)
	}
	return nil
}

func (m *DatabaseManager) ListNotebooks(db *sql.DB) ([]Notebook, error) {
	query := `SELECT id, name, created_at FROM notebooks ORDER BY created_at DESC;`
	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("unable to list notebooks: %w", err)
	}
	defer rows.Close()

	notebooks := []Notebook{}
	for rows.Next() {
		n := Notebook{}
		if err := rows.Scan(&n.ID, &n.Name, &n.CreatedAt); err != nil {
			return nil, fmt.Errorf("unable to scan notebook: %w", err)
		}
		notebooks = append(notebooks, n)
	}
	return notebooks, nil
}

func (m *DatabaseManager) GetNotebookByID(db *sql.DB, notebookID string) (*Notebook, error) {
	query := `SELECT id, name, created_at FROM notebooks WHERE id = ?;`
	notebook := &Notebook{}
	row := db.QueryRow(query, notebookID)
	if err := row.Scan(&notebook.ID, &notebook.Name, &notebook.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("unable to get notebook: %w", err)
	}
	return notebook, nil
}

func (m *DatabaseManager) GetNotesByNotebookID(db *sql.DB, notebookID string) ([]Note, error) {
	query := `SELECT id, title, content, updated_at, notebook_id FROM notes WHERE notebook_id = ? ORDER BY updated_at DESC;`
	rows, err := db.Query(query, notebookID)
	if err != nil {
		return nil, fmt.Errorf("unable to get notes by notebook: %w", err)
	}
	defer rows.Close()

	notes := []Note{}
	for rows.Next() {
		n := Note{}
		if err := rows.Scan(&n.ID, &n.Title, &n.Content, &n.UpdatedAt, &n.NotebookID); err != nil {
			return nil, fmt.Errorf("unable to scan note: %w", err)
		}
		notes = append(notes, n)
	}
	return notes, nil
}
