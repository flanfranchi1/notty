package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/flanfranchi1/notty/internal/markup"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

var ftsSanitizeRegexp = regexp.MustCompile(`[^\p{L}\p{N}\s]+`)

const tutorialBackfillSeedKey = "tutorial_showcase_backfill_v1"

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

type SearchResult struct {
	ID      string
	Title   string
	Snippet string
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

func (m *DatabaseManager) SearchNotes(db *sql.DB, queryText string) ([]SearchResult, error) {
	clean := strings.TrimSpace(ftsSanitizeRegexp.ReplaceAllString(queryText, " "))
	if clean == "" {
		return []SearchResult{}, nil
	}
	terms := strings.Fields(clean)
	for i := range terms {
		terms[i] = terms[i] + "*"
	}
	ftsQuery := strings.Join(terms, " ")

	rows, err := db.Query(`SELECT id, title, snippet(notes_fts, 2, '<b>', '</b>', '...', 10) FROM notes_fts WHERE notes_fts MATCH ? ORDER BY rank LIMIT 50;`, ftsQuery)
	if err != nil {
		return nil, fmt.Errorf("unable to search notes: %w", err)
	}
	defer rows.Close()

	seen := map[string]struct{}{}
	results := []SearchResult{}
	for rows.Next() {
		r := SearchResult{}
		if err := rows.Scan(&r.ID, &r.Title, &r.Snippet); err != nil {
			return nil, fmt.Errorf("unable to scan search result: %w", err)
		}
		if _, ok := seen[r.ID]; ok {
			continue
		}
		seen[r.ID] = struct{}{}
		results = append(results, r)
	}
	return results, nil
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

	noteTagsCleanupTrigger := `CREATE TRIGGER IF NOT EXISTS notes_tag_cleanup AFTER DELETE ON notes BEGIN DELETE FROM note_tags WHERE note_id = old.id; END;`
	if _, err := db.Exec(noteTagsCleanupTrigger); err != nil {
		return fmt.Errorf("unable to create note tags cleanup trigger: %w", err)
	}

	ftsInsertTrigger := `CREATE TRIGGER IF NOT EXISTS notes_fts_insert AFTER INSERT ON notes BEGIN INSERT INTO notes_fts(id, title, content) VALUES (new.id, new.title, new.content); END;`
	if _, err := db.Exec(ftsInsertTrigger); err != nil {
		return fmt.Errorf("unable to create fts insert trigger: %w", err)
	}

	ftsDeleteTrigger := `CREATE TRIGGER IF NOT EXISTS notes_fts_delete AFTER DELETE ON notes BEGIN DELETE FROM notes_fts WHERE id = old.id; END;`
	if _, err := db.Exec(ftsDeleteTrigger); err != nil {
		return fmt.Errorf("unable to create fts delete trigger: %w", err)
	}

	ftsUpdateTrigger := `CREATE TRIGGER IF NOT EXISTS notes_fts_update AFTER UPDATE ON notes BEGIN UPDATE notes_fts SET title = new.title, content = new.content WHERE id = new.id; END;`
	if _, err := db.Exec(ftsUpdateTrigger); err != nil {
		return fmt.Errorf("unable to create fts update trigger: %w", err)
	}

	populateFTS := `INSERT OR IGNORE INTO notes_fts(id, title, content) SELECT id, title, content FROM notes;`
	if _, err := db.Exec(populateFTS); err != nil {
		return fmt.Errorf("unable to populate fts: %w", err)
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

	tagIndexSQL := `CREATE INDEX IF NOT EXISTS idx_note_tags_tag ON note_tags(tag);`
	if _, err := db.Exec(tagIndexSQL); err != nil {
		return fmt.Errorf("unable to create tag index: %w", err)
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

func (m *DatabaseManager) ListSystemUserIDs(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT id FROM users;`)
	if err != nil {
		return nil, fmt.Errorf("unable to list system users: %w", err)
	}
	defer rows.Close()

	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("unable to scan system user id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (m *DatabaseManager) ensureSeedMetaTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS seed_meta (
		seed_key TEXT PRIMARY KEY,
		seed_value TEXT NOT NULL,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`)
	if err != nil {
		return fmt.Errorf("unable to ensure seed_meta table: %w", err)
	}
	return nil
}

func (m *DatabaseManager) seedMetaExists(db *sql.DB, key string) (bool, error) {
	var exists int
	err := db.QueryRow(`SELECT COUNT(1) FROM seed_meta WHERE seed_key = ?;`, key).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("unable to check seed_meta key %q: %w", key, err)
	}
	return exists > 0, nil
}

func (m *DatabaseManager) setSeedMeta(db *sql.DB, key, value string) error {
	_, err := db.Exec(
		`INSERT INTO seed_meta(seed_key, seed_value, updated_at)
		 VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(seed_key)
		 DO UPDATE SET seed_value = excluded.seed_value, updated_at = CURRENT_TIMESTAMP;`,
		key,
		value,
	)
	if err != nil {
		return fmt.Errorf("unable to set seed_meta key %q: %w", key, err)
	}
	return nil
}

func (m *DatabaseManager) hasTutorialNotes(db *sql.DB) (bool, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(1) FROM note_tags WHERE tag = 'tutorial';`).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("unable to detect tutorial notes: %w", err)
	}
	return count > 0, nil
}

func (m *DatabaseManager) UpdateUserPassword(db *sql.DB, userID string, newPasswordHash string) error {
	_, err := db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?;`, newPasswordHash, userID)
	if err != nil {
		return fmt.Errorf("unable to update password: %w", err)
	}
	return nil
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

// ListInboxNotes returns notes that have no notebook assignment, i.e. the
// "Inbox" — notes captured quickly without organisation.
func (m *DatabaseManager) ListInboxNotes(db *sql.DB) ([]Note, error) {
	query := `SELECT id, title, content, updated_at, COALESCE(notebook_id, '')
	          FROM notes
	          WHERE notebook_id IS NULL OR notebook_id = ''
	          ORDER BY updated_at DESC LIMIT 100;`
	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("unable to list inbox notes: %w", err)
	}
	defer rows.Close()

	notes := []Note{}
	for rows.Next() {
		n := Note{}
		if err := rows.Scan(&n.ID, &n.Title, &n.Content, &n.UpdatedAt, &n.NotebookID); err != nil {
			return nil, fmt.Errorf("unable to scan inbox note: %w", err)
		}
		notes = append(notes, n)
	}
	return notes, nil
}

// CountInboxNotes returns the number of Inbox notes (no notebook assigned).
// Used to display a badge count in the sidebar.
func (m *DatabaseManager) CountInboxNotes(db *sql.DB) (int, error) {
	var count int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM notes WHERE notebook_id IS NULL OR notebook_id = '';`,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("unable to count inbox notes: %w", err)
	}
	return count, nil
}

// GetNotebookIndexNote returns the "index note" for a notebook — the first
// note in that notebook tagged `#index`.  This note is rendered prominently
// at the top of the notebook view and its headings populate the sidebar ToC.
//
// Returns nil without error when no such note exists.
func (m *DatabaseManager) GetNotebookIndexNote(db *sql.DB, notebookID string) (*Note, error) {
	query := `
		SELECT n.id, n.title, n.content, n.updated_at, COALESCE(n.notebook_id, '')
		FROM notes n
		JOIN note_tags t ON n.id = t.note_id
		WHERE n.notebook_id = ? AND t.tag = 'index'
		ORDER BY n.updated_at ASC
		LIMIT 1;`
	note := &Note{}
	row := db.QueryRow(query, notebookID)
	if err := row.Scan(&note.ID, &note.Title, &note.Content, &note.UpdatedAt, &note.NotebookID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("unable to get notebook index note: %w", err)
	}
	return note, nil
}

func (m *DatabaseManager) GetTagsByNoteID(db *sql.DB, noteID string) ([]string, error) {
	rows, err := db.Query(`SELECT tag FROM note_tags WHERE note_id = ? ORDER BY tag ASC`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

func (m *DatabaseManager) ListAllTags(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT DISTINCT tag FROM note_tags ORDER BY tag ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

func (m *DatabaseManager) GetNotesByTag(db *sql.DB, tag string) ([]Note, error) {
	rows, err := db.Query(`
		SELECT n.id, n.title, n.content, n.notebook_id, n.updated_at
		FROM notes n
		JOIN note_tags t ON n.id = t.note_id
		WHERE t.tag = ?
		ORDER BY n.updated_at DESC`, tag)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notes []Note
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.Title, &n.Content, &n.NotebookID, &n.UpdatedAt); err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}

// SeedTutorial populates a new user's database with a "Tutorial" notebook
// containing three notes that showcase the Notty Markdown parser features.
// translations is the flat key→value map for the user's locale, obtained via
// bundle.Translations(locale). The call is intentionally non-destructive: if
// any step fails the error is returned and the caller may choose to log and
// continue rather than blocking account creation.
func (m *DatabaseManager) SeedTutorial(db *sql.DB, translations map[string]string) error {
	tr := func(key string) string {
		if v, ok := translations[key]; ok {
			return v
		}
		return key
	}

	notebook := Notebook{
		ID:   uuid.NewString(),
		Name: tr("tutorial.notebook.name"),
	}
	if err := m.CreateNotebook(db, notebook); err != nil {
		return fmt.Errorf("SeedTutorial: create notebook: %w", err)
	}

	tutorialNotes := []Note{
		{
			ID:         uuid.NewString(),
			Title:      tr("tutorial.note1.title"),
			Content:    tr("tutorial.markdown.formatting"),
			NotebookID: notebook.ID,
		},
		{
			ID:         uuid.NewString(),
			Title:      tr("tutorial.note2.title"),
			Content:    tr("tutorial.markdown.advanced"),
			NotebookID: notebook.ID,
		},
		{
			ID:         uuid.NewString(),
			Title:      tr("tutorial.note3.title"),
			Content:    tr("tutorial.markdown.wikilinks"),
			NotebookID: notebook.ID,
		},
	}

	for _, note := range tutorialNotes {
		if err := m.CreateNote(db, note); err != nil {
			return fmt.Errorf("SeedTutorial: create note %q: %w", note.Title, err)
		}
	}

	// Resolve wikilinks now that all tutorial notes exist, then insert tags.
	for _, note := range tutorialNotes {
		linkedTitles := markup.ParseWikiLinks(note.Content)
		targetIDs := make([]string, 0, len(linkedTitles))
		for _, title := range linkedTitles {
			target, err := m.GetNoteByTitle(db, title)
			if err != nil || target == nil {
				continue
			}
			targetIDs = append(targetIDs, target.ID)
		}
		if len(targetIDs) > 0 {
			if err := m.InsertNoteLinks(db, note.ID, targetIDs); err != nil {
				return fmt.Errorf("SeedTutorial: insert links for %q: %w", note.Title, err)
			}
		}

		tags := markup.ParseTags(note.Content)
		if len(tags) > 0 {
			if err := m.InsertNoteTags(db, note.ID, tags); err != nil {
				return fmt.Errorf("SeedTutorial: insert tags for %q: %w", note.Title, err)
			}
		}
	}

	return nil
}

// BackfillTutorialShowcase seeds the tutorial showcase for existing users who
// do not have tutorial-tagged notes yet.
//
// Idempotency rules:
//   - each user DB gets a seed marker in seed_meta using tutorialBackfillSeedKey
//   - reruns skip already-processed user DBs
//
// Users that already have tutorial notes are marked as skipped to avoid adding
// duplicate tutorial notebooks into accounts that likely already received one.
func (m *DatabaseManager) BackfillTutorialShowcase(systemDB *sql.DB, translations map[string]string) (int, int, error) {
	userIDs, err := m.ListSystemUserIDs(systemDB)
	if err != nil {
		return 0, 0, fmt.Errorf("BackfillTutorialShowcase: list users: %w", err)
	}

	seeded := 0
	skipped := 0

	for _, userID := range userIDs {
		userDB, err := m.OpenUserDB(userID)
		if err != nil {
			return seeded, skipped, fmt.Errorf("BackfillTutorialShowcase: open user db %s: %w", userID, err)
		}

		if err := m.ensureSeedMetaTable(userDB); err != nil {
			userDB.Close()
			return seeded, skipped, fmt.Errorf("BackfillTutorialShowcase: ensure seed_meta for %s: %w", userID, err)
		}

		alreadyProcessed, err := m.seedMetaExists(userDB, tutorialBackfillSeedKey)
		if err != nil {
			userDB.Close()
			return seeded, skipped, fmt.Errorf("BackfillTutorialShowcase: check seed marker for %s: %w", userID, err)
		}
		if alreadyProcessed {
			skipped++
			userDB.Close()
			continue
		}

		hasTutorial, err := m.hasTutorialNotes(userDB)
		if err != nil {
			userDB.Close()
			return seeded, skipped, fmt.Errorf("BackfillTutorialShowcase: detect tutorial for %s: %w", userID, err)
		}

		if hasTutorial {
			if err := m.setSeedMeta(userDB, tutorialBackfillSeedKey, "already_had_tutorial"); err != nil {
				userDB.Close()
				return seeded, skipped, fmt.Errorf("BackfillTutorialShowcase: set skip marker for %s: %w", userID, err)
			}
			skipped++
			userDB.Close()
			continue
		}

		if err := m.SeedTutorial(userDB, translations); err != nil {
			userDB.Close()
			return seeded, skipped, fmt.Errorf("BackfillTutorialShowcase: seed tutorial for %s: %w", userID, err)
		}

		if err := m.setSeedMeta(userDB, tutorialBackfillSeedKey, "seeded"); err != nil {
			userDB.Close()
			return seeded, skipped, fmt.Errorf("BackfillTutorialShowcase: set seed marker for %s: %w", userID, err)
		}

		seeded++
		userDB.Close()
	}

	return seeded, skipped, nil
}
