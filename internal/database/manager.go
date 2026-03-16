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
	ID        string
	Title     string
	Content   string
	UpdatedAt string
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

	insert := `INSERT INTO notes (id, title, content) VALUES (?, ?, ?);`
	if _, err = tx.Exec(insert, note.ID, note.Title, note.Content); err != nil {
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
	query := `SELECT id, title, content, updated_at FROM notes ORDER BY updated_at DESC LIMIT 100;`
	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("unable to list notes: %w", err)
	}
	defer rows.Close()

	notes := []Note{}
	for rows.Next() {
		n := Note{}
		if err := rows.Scan(&n.ID, &n.Title, &n.Content, &n.UpdatedAt); err != nil {
			return nil, fmt.Errorf("unable to scan note: %w", err)
		}
		notes = append(notes, n)
	}
	return notes, nil
}

func (m *DatabaseManager) GetNoteByID(db *sql.DB, noteID string) (*Note, error) {
	query := `SELECT id, title, content, updated_at FROM notes WHERE id = ?;`
	note := &Note{}
	row := db.QueryRow(query, noteID)
	if err := row.Scan(&note.ID, &note.Title, &note.Content, &note.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("unable to get note: %w", err)
	}
	return note, nil
}

func (m *DatabaseManager) GetNoteByTitle(db *sql.DB, title string) (*Note, error) {
	query := `SELECT id, title, content, updated_at FROM notes WHERE lower(title) = lower(?) LIMIT 1;`
	note := &Note{}
	row := db.QueryRow(query, title)
	if err := row.Scan(&note.ID, &note.Title, &note.Content, &note.UpdatedAt); err != nil {
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

	update := `UPDATE notes SET title = ?, content = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;`
	if _, err = tx.Exec(update, note.Title, note.Content, note.ID); err != nil {
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
