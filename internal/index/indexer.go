package index

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Indexer struct {
	codexHome  string
	claudeHome string
	dbPath     string
	db         *sql.DB
	ftsEnabled bool
	mu         sync.Mutex
}

func New(codexHome, claudeHome, dbPath string, reindex bool) (*Indexer, error) {
	if reindex {
		_ = os.Remove(dbPath)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	i := &Indexer{codexHome: codexHome, claudeHome: claudeHome, dbPath: dbPath, db: db}
	if err := i.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return i, nil
}

func (i *Indexer) Close() error {
	return i.db.Close()
}

func (i *Indexer) initSchema() error {
	stmts := []string{
		`PRAGMA journal_mode = WAL;`,
		`PRAGMA foreign_keys = ON;`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			source TEXT,
			last_activity_ts INTEGER,
			message_count INTEGER,
			workdir TEXT,
			preview TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT,
			ts INTEGER,
			role TEXT,
			content TEXT,
			type TEXT,
			source TEXT,
			source_path TEXT,
			workdir TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session_id ON messages(session_id);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session_ts ON messages(session_id, ts, id);`,
		`CREATE TABLE IF NOT EXISTS ingested_files (
			path TEXT PRIMARY KEY,
			mtime INTEGER,
			size INTEGER,
			offset INTEGER,
			source TEXT
		);`,
	}

	for _, stmt := range stmts {
		if _, err := i.db.Exec(stmt); err != nil {
			return fmt.Errorf("init schema: %w", err)
		}
	}
	return i.ensureFTSTable()
}

func (i *Indexer) ensureFTSTable() error {
	var sqlDef string
	err := i.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'messages_fts'`).Scan(&sqlDef)
	if err == nil {
		lower := strings.ToLower(sqlDef)
		i.ftsEnabled = strings.Contains(lower, "virtual table") && strings.Contains(lower, "fts5")
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("inspect messages_fts table: %w", err)
	}

	_, err = i.db.Exec(`CREATE VIRTUAL TABLE messages_fts USING fts5(
		session_id UNINDEXED,
		role UNINDEXED,
		content
	);`)
	if err == nil {
		i.ftsEnabled = true
		return nil
	}

	if !strings.Contains(strings.ToLower(err.Error()), "no such module: fts5") {
		return fmt.Errorf("create messages_fts: %w", err)
	}

	// Fallback for sqlite builds without FTS5 support.
	if _, err := i.db.Exec(`CREATE TABLE IF NOT EXISTS messages_fts (
		rowid INTEGER PRIMARY KEY,
		session_id TEXT,
		role TEXT,
		content TEXT
	);`); err != nil {
		return fmt.Errorf("create messages_fts fallback table: %w", err)
	}
	if _, err := i.db.Exec(`CREATE INDEX IF NOT EXISTS idx_messages_fts_session_id ON messages_fts(session_id);`); err != nil {
		return fmt.Errorf("create fallback messages_fts index: %w", err)
	}
	i.ftsEnabled = false
	return nil
}

func (i *Indexer) BuildIndex(ctx context.Context) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	sources, err := discoverAllSources(i.codexHome, i.claudeHome)
	if err != nil {
		return fmt.Errorf("discover sources: %w", err)
	}
	if err := i.pruneMissingSources(ctx, sources); err != nil {
		return err
	}
	if len(sources) == 0 {
		if err := i.refreshSessions(ctx); err != nil {
			return err
		}
		return nil
	}

	for _, src := range sources {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := i.ingestFile(ctx, src); err != nil {
			return err
		}
	}

	return i.refreshSessions(ctx)
}

type fileMeta struct {
	Mtime  int64
	Size   int64
	Offset int64
}

func (i *Indexer) ingestFile(ctx context.Context, src sourceFile) error {
	stat, err := os.Stat(src.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", src.Path, err)
	}

	meta, found, err := i.getIngestedMeta(src.Path)
	if err != nil {
		return err
	}

	var offset int64
	needsReset := false
	if found {
		offset = meta.Offset
		if stat.Size() < meta.Offset ||
			stat.ModTime().Unix() < meta.Mtime ||
			(stat.ModTime().Unix() != meta.Mtime && stat.Size() == meta.Size) {
			needsReset = true
			offset = 0
		}
	}
	if !found {
		offset = 0
	}

	file, err := os.Open(src.Path)
	if err != nil {
		return fmt.Errorf("open %s: %w", src.Path, err)
	}
	defer file.Close()

	if _, err := file.Seek(offset, 0); err != nil {
		return fmt.Errorf("seek %s: %w", src.Path, err)
	}

	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin ingest tx: %w", err)
	}
	defer tx.Rollback()

	if needsReset {
		if _, err := tx.ExecContext(ctx, `DELETE FROM messages_fts WHERE rowid IN (SELECT id FROM messages WHERE source_path = ?);`, src.Path); err != nil {
			return fmt.Errorf("clear stale fts rows for %s: %w", src.Path, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE source_path = ?;`, src.Path); err != nil {
			return fmt.Errorf("clear stale rows for %s: %w", src.Path, err)
		}
	}

	insertMsgStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO messages(session_id, ts, role, content, type, source, source_path, workdir)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare message insert: %w", err)
	}
	defer insertMsgStmt.Close()

	insertFTSStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO messages_fts(rowid, session_id, role, content)
		VALUES(?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare fts insert: %w", err)
	}
	defer insertFTSStmt.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Bytes()
		var events []parsedEvent
		if src.Source == "claude" {
			events, err = parseClaudeJSONLLine(line, src.Path)
		} else {
			events, err = parseJSONLLine(line, src.Path)
		}
		if err != nil {
			continue
		}
		for _, evt := range events {
			if strings.TrimSpace(evt.Content) == "" {
				continue
			}
			sessionID := strings.TrimSpace(evt.SessionID)
			if sessionID == "" {
				sessionID = inferSessionIDFromPath(src.Path)
			}

			res, err := insertMsgStmt.ExecContext(ctx,
				sessionID,
				nullableTS(evt.TS),
				evt.Role,
				evt.Content,
				evt.Type,
				src.Source,
				src.Path,
				evt.Workdir,
			)
			if err != nil {
				continue
			}
			rowID, err := res.LastInsertId()
			if err != nil {
				continue
			}
			_, _ = insertFTSStmt.ExecContext(ctx, rowID, sessionID, evt.Role, evt.Content)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan %s: %w", src.Path, err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ingested_files(path, mtime, size, offset, source)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			mtime=excluded.mtime,
			size=excluded.size,
			offset=excluded.offset,
			source=excluded.source
	`, src.Path, stat.ModTime().Unix(), stat.Size(), stat.Size(), src.Source); err != nil {
		return fmt.Errorf("update ingested file metadata: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit ingest %s: %w", src.Path, err)
	}
	return nil
}

func (i *Indexer) getIngestedMeta(path string) (fileMeta, bool, error) {
	row := i.db.QueryRow(`SELECT mtime, size, offset FROM ingested_files WHERE path = ?`, path)
	var meta fileMeta
	if err := row.Scan(&meta.Mtime, &meta.Size, &meta.Offset); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fileMeta{}, false, nil
		}
		return fileMeta{}, false, fmt.Errorf("read ingested metadata for %s: %w", path, err)
	}
	return meta, true, nil
}

func (i *Indexer) pruneMissingSources(ctx context.Context, sources []sourceFile) error {
	keep := make(map[string]struct{}, len(sources))
	for _, src := range sources {
		keep[src.Path] = struct{}{}
	}

	rows, err := i.db.QueryContext(ctx, `SELECT path FROM ingested_files`)
	if err != nil {
		return fmt.Errorf("query ingested files: %w", err)
	}
	defer rows.Close()

	var stale []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return fmt.Errorf("scan ingested file row: %w", err)
		}
		if _, ok := keep[path]; !ok {
			stale = append(stale, path)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate ingested files: %w", err)
	}
	if len(stale) == 0 {
		return nil
	}

	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin stale-source cleanup tx: %w", err)
	}
	defer tx.Rollback()

	for _, path := range stale {
		if _, err := tx.ExecContext(ctx, `DELETE FROM messages_fts WHERE rowid IN (SELECT id FROM messages WHERE source_path = ?)`, path); err != nil {
			return fmt.Errorf("delete stale fts for %s: %w", path, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE source_path = ?`, path); err != nil {
			return fmt.Errorf("delete stale messages for %s: %w", path, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM ingested_files WHERE path = ?`, path); err != nil {
			return fmt.Errorf("delete stale ingested metadata for %s: %w", path, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit stale-source cleanup: %w", err)
	}
	return nil
}

func nullableTS(ts *int64) any {
	if ts == nil {
		return nil
	}
	return *ts
}

func inferSessionIDFromPath(path string) string {
	return sessionIDFromPath(path)
}

func (i *Indexer) refreshSessions(ctx context.Context) error {
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin refresh sessions tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions;`); err != nil {
		return fmt.Errorf("clear sessions: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT session_id FROM messages ORDER BY session_id;`)
	if err != nil {
		return fmt.Errorf("list distinct session ids: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			return fmt.Errorf("scan distinct session id: %w", err)
		}

		session, err := i.computeSessionSummary(ctx, tx, sessionID)
		if err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO sessions(id, source, last_activity_ts, message_count, workdir, preview)
			VALUES(?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				source=excluded.source,
				last_activity_ts=excluded.last_activity_ts,
				message_count=excluded.message_count,
				workdir=excluded.workdir,
				preview=excluded.preview
		`, session.ID, session.Source, session.LastActivityTS, session.MessageCount, session.Workdir, session.Preview); err != nil {
			return fmt.Errorf("upsert session %s: %w", session.ID, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate session ids: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit refresh sessions: %w", err)
	}
	return nil
}

func (i *Indexer) computeSessionSummary(ctx context.Context, tx *sql.Tx, sessionID string) (Session, error) {
	session := Session{ID: sessionID}

	row := tx.QueryRowContext(ctx, `
		SELECT
			COALESCE(MAX(COALESCE(ts, 0)), 0) AS last_ts,
			COALESCE((SELECT source FROM messages m2 WHERE m2.session_id = ? ORDER BY m2.id DESC LIMIT 1), 'unknown')
		FROM messages
		WHERE session_id = ?
	`, sessionID, sessionID)

	if err := row.Scan(&session.LastActivityTS, &session.Source); err != nil {
		return session, fmt.Errorf("summary for session %s: %w", sessionID, err)
	}
	hasRealUser, err := hasRealUserMessage(ctx, tx, sessionID)
	if err != nil {
		return session, fmt.Errorf("real-user check for session %s: %w", sessionID, err)
	}
	if hasRealUser {
		session.MessageCount = countConversationalMessages(ctx, tx, sessionID)
	} else {
		session.MessageCount = 0
	}

	_ = tx.QueryRowContext(ctx, `
		SELECT workdir FROM messages
		WHERE session_id = ? AND workdir IS NOT NULL AND workdir != ''
		ORDER BY id DESC
		LIMIT 1
	`, sessionID).Scan(&session.Workdir)
	if session.Workdir == "" {
		if inferred, err := inferWorkdirFromSessionContent(ctx, tx, sessionID); err == nil {
			session.Workdir = inferred
		}
	}
	if session.Workdir == "" && session.Source == "claude" {
		var sourcePath string
		_ = tx.QueryRowContext(ctx, `
			SELECT source_path FROM messages
			WHERE session_id = ? AND source_path IS NOT NULL AND source_path != ''
			LIMIT 1
		`, sessionID).Scan(&sourcePath)
		if sourcePath != "" {
			session.Workdir = workdirFromClaudePath(sourcePath)
		}
	}
	session.Preview = trimPreview(pickSessionPreview(ctx, tx, sessionID))
	return session, nil
}

func trimPreview(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= 120 {
		return s
	}
	return s[:117] + "..."
}

func pickSessionPreview(ctx context.Context, tx *sql.Tx, sessionID string) string {
	queries := []struct {
		sql  string
		args []any
	}{
		{
			sql: `
				SELECT content FROM messages
				WHERE session_id = ? AND role = 'user' AND type = 'message'
				ORDER BY id ASC
				LIMIT 40
			`,
			args: []any{sessionID},
		},
		{
			sql: `
				SELECT content FROM messages
				WHERE session_id = ? AND role = 'user'
				ORDER BY id DESC
				LIMIT 40
			`,
			args: []any{sessionID},
		},
	}

	for _, q := range queries {
		rows, err := tx.QueryContext(ctx, q.sql, q.args...)
		if err != nil {
			continue
		}
		var candidate string
		for rows.Next() {
			if err := rows.Scan(&candidate); err != nil {
				continue
			}
			if isNonConversationalPreviewContent(candidate) {
				continue
			}
			_ = rows.Close()
			return candidate
		}
		_ = rows.Close()
	}
	return ""
}

func countConversationalMessages(ctx context.Context, tx *sql.Tx, sessionID string) int {
	rows, err := tx.QueryContext(ctx, `
		SELECT role, content
		FROM messages
		WHERE session_id = ? AND type = 'message' AND role IN ('user', 'assistant')
	`, sessionID)
	if err != nil {
		return 0
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var role, content string
		if err := rows.Scan(&role, &content); err != nil {
			continue
		}
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		if role == "assistant" {
			count++
			continue
		}
		if role == "user" && !isNonConversationalPreviewContent(content) {
			count++
		}
	}
	return count
}

func hasRealUserMessage(ctx context.Context, tx *sql.Tx, sessionID string) (bool, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT content
		FROM messages
		WHERE session_id = ? AND type = 'message' AND role = 'user'
	`, sessionID)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			continue
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		if !isNonConversationalPreviewContent(content) {
			return true, nil
		}
	}
	return false, rows.Err()
}

func inferWorkdirFromSessionContent(ctx context.Context, tx *sql.Tx, sessionID string) (string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT content FROM messages
		WHERE session_id = ? AND role = 'user'
		ORDER BY id ASC
		LIMIT 40
	`, sessionID)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			continue
		}
		if wd := extractWorkdirFromContent(content); wd != "" {
			return wd, nil
		}
	}
	return "", rows.Err()
}

func extractWorkdirFromContent(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	lower := strings.ToLower(content)

	if start := strings.Index(lower, "<cwd>"); start >= 0 {
		start += len("<cwd>")
		endRel := strings.Index(lower[start:], "</cwd>")
		if endRel >= 0 {
			if wd := strings.TrimSpace(content[start : start+endRel]); looksLikePath(wd) {
				return wd
			}
		}
	}

	if strings.HasPrefix(lower, "<environment_context>") {
		inner := content
		inner = strings.TrimPrefix(inner, "<environment_context>")
		innerLower := strings.ToLower(inner)
		if end := strings.Index(innerLower, "</environment_context>"); end >= 0 {
			inner = inner[:end]
		}
		for _, token := range strings.Fields(inner) {
			if looksLikePath(token) {
				return token
			}
		}
	}

	return ""
}

func looksLikePath(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~/")
}

func (i *Indexer) ListSessions(query string, limit int) ([]Session, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if limit <= 0 {
		limit = 200
	}
	query = strings.TrimSpace(query)

	var rows *sql.Rows
	var err error
	if query == "" {
		rows, err = i.db.Query(`
			SELECT id, source, COALESCE(last_activity_ts, 0), COALESCE(message_count, 0), COALESCE(workdir, ''), COALESCE(preview, '')
			FROM sessions
			WHERE COALESCE(message_count, 0) > 0
			ORDER BY last_activity_ts DESC, id
			LIMIT ?
		`, limit)
	} else {
		rows, err = i.searchRows(query, limit)
		if err != nil {
			return nil, err
		}
	}
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	out := make([]Session, 0, 128)
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.Source, &s.LastActivityTS, &s.MessageCount, &s.Workdir, &s.Preview); err != nil {
			return nil, fmt.Errorf("scan session row: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session rows: %w", err)
	}
	return out, nil
}

func (i *Indexer) searchRows(query string, limit int) (*sql.Rows, error) {
	if i.ftsEnabled {
		rows, err := i.searchRowsFTS(query, limit)
		if err == nil {
			return rows, nil
		}
		fallback, fbErr := i.searchRowsLike(query, limit)
		if fbErr != nil {
			return nil, fmt.Errorf("list sessions search (fts and fallback failed): fts=%w, fallback=%v", err, fbErr)
		}
		return fallback, nil
	}
	return i.searchRowsLike(query, limit)
}

func (i *Indexer) searchRowsFTS(query string, limit int) (*sql.Rows, error) {
	ftsQuery := buildFTSQuery(query)
	if ftsQuery == "" {
		return nil, fmt.Errorf("empty fts query")
	}
	rows, err := i.db.Query(`
		SELECT s.id, s.source, COALESCE(s.last_activity_ts, 0), COALESCE(s.message_count, 0), COALESCE(s.workdir, ''), COALESCE(s.preview, '')
		FROM sessions s
		JOIN (
			SELECT session_id, COUNT(*) AS score
			FROM messages_fts
			WHERE messages_fts MATCH ?
			GROUP BY session_id
			ORDER BY score DESC
			LIMIT ?
		) ranked ON ranked.session_id = s.id
		WHERE COALESCE(s.message_count, 0) > 0
		ORDER BY ranked.score DESC, s.last_activity_ts DESC
	`, ftsQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("fts query failed: %w", err)
	}
	return rows, nil
}

func (i *Indexer) searchRowsLike(query string, limit int) (*sql.Rows, error) {
	terms := tokenizeSearchTerms(query)
	if len(terms) == 0 {
		terms = []string{strings.ToLower(strings.TrimSpace(query))}
	}
	if len(terms) == 0 || terms[0] == "" {
		terms = []string{""}
	}

	var b strings.Builder
	b.WriteString(`
		SELECT s.id, s.source, COALESCE(s.last_activity_ts, 0), COALESCE(s.message_count, 0), COALESCE(s.workdir, ''), COALESCE(s.preview, '')
		FROM sessions s
		JOIN (
			SELECT session_id, COUNT(*) AS score
			FROM messages
			WHERE `)
	args := make([]any, 0, len(terms)+1)
	for idx, term := range terms {
		if idx > 0 {
			b.WriteString(" OR ")
		}
		b.WriteString("LOWER(content) LIKE ?")
		args = append(args, "%"+term+"%")
	}
	b.WriteString(`
			GROUP BY session_id
			ORDER BY score DESC
			LIMIT ?
		) ranked ON ranked.session_id = s.id
		WHERE COALESCE(s.message_count, 0) > 0
		ORDER BY ranked.score DESC, s.last_activity_ts DESC
	`)
	args = append(args, limit)
	rows, err := i.db.Query(b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("like query failed: %w", err)
	}
	return rows, nil
}

func buildFTSQuery(raw string) string {
	parts := tokenizeSearchTerms(raw)
	if len(parts) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = strings.ReplaceAll(p, `"`, "")
		quoted = append(quoted, fmt.Sprintf(`"%s"*`, p))
	}
	return strings.Join(quoted, " AND ")
}

func tokenizeSearchTerms(raw string) []string {
	parts := strings.Fields(strings.ToLower(strings.TrimSpace(raw)))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, "`\"'.,:;!?()[]{}<>|")
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func (i *Indexer) GetSession(sessionID string) (Session, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	var s Session
	err := i.db.QueryRow(`
		SELECT id, source, COALESCE(last_activity_ts, 0), COALESCE(message_count, 0), COALESCE(workdir, ''), COALESCE(preview, '')
		FROM sessions WHERE id = ?
	`, sessionID).Scan(&s.ID, &s.Source, &s.LastActivityTS, &s.MessageCount, &s.Workdir, &s.Preview)
	if err != nil {
		return Session{}, err
	}
	return s, nil
}

func (i *Indexer) GetMessages(sessionID string) ([]Message, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	rows, err := i.db.Query(`
		SELECT id, session_id, ts, role, content, type, source, source_path, COALESCE(workdir, '')
		FROM messages
		WHERE session_id = ?
		ORDER BY CASE WHEN ts IS NULL THEN 1 ELSE 0 END, ts, id
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query session messages: %w", err)
	}
	defer rows.Close()

	out := make([]Message, 0, 256)
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.SessionID, &m.TS, &m.Role, &m.Content, &m.Type, &m.Source, &m.SourcePath, &m.Workdir); err != nil {
			return nil, fmt.Errorf("scan message row: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages: %w", err)
	}
	return out, nil
}

func FormatUnix(ts int64) string {
	if ts <= 0 {
		return "n/a"
	}
	return time.Unix(ts, 0).Local().Format("2006-01-02 15:04")
}
