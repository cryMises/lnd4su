package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS problems (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  slug          TEXT NOT NULL UNIQUE,
  title         TEXT NOT NULL,
  statement_pdf TEXT,
  samples_json  TEXT NOT NULL DEFAULT '[]',
  tags          TEXT NOT NULL DEFAULT '',
  difficulty    TEXT NOT NULL DEFAULT 'Mudah',
  time_limit_ms INTEGER NOT NULL DEFAULT 1000,
  memory_limit_kb INTEGER NOT NULL DEFAULT 65536,
  compare_mode  TEXT NOT NULL DEFAULT 'tolerant',
  wiki_page_id  TEXT NOT NULL DEFAULT '',
  created_by    TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS test_cases (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  problem_id      INTEGER NOT NULL REFERENCES problems(id) ON DELETE CASCADE,
  position        INTEGER NOT NULL,
  stdin           TEXT NOT NULL DEFAULT '',
  expected_stdout TEXT NOT NULL DEFAULT '',
  subtask         TEXT NOT NULL DEFAULT '',
  UNIQUE(problem_id, position)
);
CREATE TABLE IF NOT EXISTS submissions (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  problem_id    INTEGER NOT NULL REFERENCES problems(id) ON DELETE CASCADE,
  username      TEXT NOT NULL,
  language      TEXT NOT NULL,
  source        TEXT NOT NULL,
  status        TEXT NOT NULL DEFAULT 'queued',
  verdict       TEXT NOT NULL DEFAULT '',
  passed_count  INTEGER NOT NULL DEFAULT 0,
  per_case_json TEXT NOT NULL DEFAULT '[]',
  compile_log   TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL,
  finished_at   INTEGER
);
CREATE INDEX IF NOT EXISTS idx_sub_user ON submissions(username, id DESC);
CREATE INDEX IF NOT EXISTS idx_sub_problem ON submissions(problem_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_sub_queued ON submissions(status);
CREATE TABLE IF NOT EXISTS sessions (
  token      TEXT PRIMARY KEY,
  username   TEXT NOT NULL,
  is_admin   INTEGER NOT NULL DEFAULT 0,
  expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS blocked_users (
  username   TEXT PRIMARY KEY,
  reason     TEXT NOT NULL DEFAULT '',
  blocked_by TEXT NOT NULL DEFAULT '',
  blocked_at INTEGER NOT NULL
);
`

func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// one connection: SQLite is a single writer and load here is tiny.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func unix(t time.Time) int64 { return t.Unix() }

// ---- problems ----

type Sample struct {
	Input  string `json:"input"`
	Output string `json:"output"`
}

type Problem struct {
	ID            int64
	Slug          string
	Title         string
	HasPDF        bool
	Samples       []Sample
	Tags          string
	Difficulty    string
	TimeLimitMS   int
	MemoryLimitKB int
	CompareMode   string
	WikiPageID    string
	CreatedBy     string
	CreatedAt     time.Time

	// list decorations (not stored)
	SolvedCount   int
	Attempted     bool
	Solved        bool
	HasUnresolved bool
}

type TestCase struct {
	Position int
	Stdin    string
	Expected string
	Subtask  string
}

var ErrNotFound = errors.New("not found")

func (s *Store) ListProblems() ([]Problem, error) {
	rows, err := s.db.Query(`SELECT id, slug, title, statement_pdf IS NOT NULL, tags, difficulty,
		time_limit_ms, memory_limit_kb, compare_mode, wiki_page_id, created_by, created_at
		FROM problems ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Problem
	for rows.Next() {
		var p Problem
		var created int64
		if err := rows.Scan(&p.ID, &p.Slug, &p.Title, &p.HasPDF, &p.Tags, &p.Difficulty,
			&p.TimeLimitMS, &p.MemoryLimitKB, &p.CompareMode, &p.WikiPageID, &p.CreatedBy, &created); err != nil {
			return nil, err
		}
		p.CreatedAt = time.Unix(created, 0)
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkSolved decorates problems with the given user's progress.
func (s *Store) MarkSolved(ps []Problem, username string) error {
	rows, err := s.db.Query(`SELECT DISTINCT problem_id,
		MAX(CASE WHEN verdict='AC' THEN 1 ELSE 0 END) AS solved
		FROM submissions WHERE username=? AND status='done' GROUP BY problem_id`, username)
	if err != nil {
		return err
	}
	defer rows.Close()
	type st struct{ attempted, solved bool }
	m := map[int64]st{}
	for rows.Next() {
		var id int64
		var solved int
		if err := rows.Scan(&id, &solved); err != nil {
			return err
		}
		m[id] = st{attempted: true, solved: solved == 1}
	}
	for i := range ps {
		if v, ok := m[ps[i].ID]; ok {
			ps[i].Attempted, ps[i].Solved = v.attempted, v.solved
		}
	}
	return nil
}

func (s *Store) SolveCounts() (map[int64]int, error) {
	rows, err := s.db.Query(`SELECT problem_id, COUNT(DISTINCT username) FROM submissions
		WHERE status='done' AND verdict='AC' GROUP BY problem_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		m[id] = n
	}
	return m, rows.Err()
}

func (s *Store) ProblemBySlug(slug string) (*Problem, error) {
	var p Problem
	var created int64
	var samples string
	err := s.db.QueryRow(`SELECT id, slug, title, statement_pdf IS NOT NULL, samples_json, tags, difficulty,
		time_limit_ms, memory_limit_kb, compare_mode, wiki_page_id, created_by, created_at
		FROM problems WHERE slug=?`, slug).Scan(&p.ID, &p.Slug, &p.Title, &p.HasPDF, &samples, &p.Tags,
		&p.Difficulty, &p.TimeLimitMS, &p.MemoryLimitKB, &p.CompareMode, &p.WikiPageID, &p.CreatedBy, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.CreatedAt = time.Unix(created, 0)
	_ = jsonUnmarshal(samples, &p.Samples)
	return &p, nil
}

func (s *Store) ProblemByID(id int64) (*Problem, error) {
	var slug string
	if err := s.db.QueryRow(`SELECT slug FROM problems WHERE id=?`, id).Scan(&slug); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return s.ProblemBySlug(slug)
}

// CreateProblem inserts a problem plus its 10 cases atomically.
func (s *Store) CreateProblem(p *Problem, cases []TestCase) (int64, error) {
	if len(cases) != 10 {
		return 0, fmt.Errorf("problem must have exactly 10 test cases, got %d", len(cases))
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := unix(time.Now())
	res, err := tx.Exec(`INSERT INTO problems (slug, title, samples_json, tags, difficulty,
		time_limit_ms, memory_limit_kb, compare_mode, wiki_page_id, created_by, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.Slug, p.Title, jsonMarshal(p.Samples), p.Tags, p.Difficulty,
		p.TimeLimitMS, p.MemoryLimitKB, p.CompareMode, p.WikiPageID, p.CreatedBy, now, now)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if err := insertCases(tx, id, cases); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func insertCases(tx *sql.Tx, problemID int64, cases []TestCase) error {
	for i, c := range cases {
		if _, err := tx.Exec(`INSERT INTO test_cases (problem_id, position, stdin, expected_stdout, subtask)
			VALUES (?,?,?,?,?)`, problemID, i+1, c.Stdin, c.Expected, c.Subtask); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) UpdateProblem(p *Problem, cases []TestCase) error {
	if len(cases) != 10 {
		return fmt.Errorf("problem must have exactly 10 test cases, got %d", len(cases))
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`UPDATE problems SET title=?, samples_json=?, tags=?, difficulty=?,
		time_limit_ms=?, memory_limit_kb=?, compare_mode=?, wiki_page_id=?, updated_at=? WHERE id=?`,
		p.Title, jsonMarshal(p.Samples), p.Tags, p.Difficulty, p.TimeLimitMS,
		p.MemoryLimitKB, p.CompareMode, p.WikiPageID, unix(time.Now()), p.ID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM test_cases WHERE problem_id=?`, p.ID); err != nil {
		return err
	}
	if err := insertCases(tx, p.ID, cases); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetStatement(id int64, filename string) error {
	var v any
	if filename != "" {
		v = filename
	}
	_, err := s.db.Exec(`UPDATE problems SET statement_pdf=?, updated_at=? WHERE id=?`, v, unix(time.Now()), id)
	return err
}

func (s *Store) SetCompileLog(id int64, log string) error {
	_, err := s.db.Exec(`UPDATE submissions SET compile_log=? WHERE id=?`, log, id)
	return err
}

func (s *Store) StatementFile(id int64) (string, bool) {
	var f sql.NullString
	if err := s.db.QueryRow(`SELECT statement_pdf FROM problems WHERE id=?`, id).Scan(&f); err != nil || !f.Valid {
		return "", false
	}
	return f.String, true
}

func (s *Store) DeleteProblem(id int64) error {
	_, err := s.db.Exec(`DELETE FROM problems WHERE id=?`, id)
	return err
}

func (s *Store) Cases(problemID int64) ([]TestCase, error) {
	rows, err := s.db.Query(`SELECT position, stdin, expected_stdout, subtask FROM test_cases
		WHERE problem_id=? ORDER BY position`, problemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TestCase
	for rows.Next() {
		var c TestCase
		if err := rows.Scan(&c.Position, &c.Stdin, &c.Expected, &c.Subtask); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) UpdateCaseExpected(problemID int64, position int, expected string) error {
	_, err := s.db.Exec(`UPDATE test_cases SET expected_stdout=? WHERE problem_id=? AND position=?`,
		expected, problemID, position)
	return err
}

// ---- submissions ----

type CaseResult struct {
	Position  int    `json:"position"`
	Verdict   string `json:"verdict"`
	TimeMS    int64  `json:"time_ms"`
	MemoryKB  int64  `json:"memory_kb"`
	Stdin     string `json:"stdin,omitempty"`
	Expected  string `json:"expected,omitempty"`
	Actual    string `json:"actual,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type Submission struct {
	ID          int64
	ProblemID   int64
	Username    string
	Language    string
	Source      string
	Status      string
	Verdict     string
	PassedCount int
	CompileLog  string
	Cases       []CaseResult
	CreatedAt   time.Time
	FinishedAt  *time.Time
}

func (s *Store) CreateSubmission(problemID int64, username, lang, source string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO submissions (problem_id, username, language, source, status, created_at)
		VALUES (?,?,?,?,'queued',?)`, problemID, username, lang, source, unix(time.Now()))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) SubmissionByID(id int64) (*Submission, error) {
	var sub Submission
	var created int64
	var cases string
	var finished sql.NullInt64
	err := s.db.QueryRow(`SELECT id, problem_id, username, language, source, status, verdict,
		passed_count, per_case_json, compile_log, created_at, finished_at FROM submissions WHERE id=?`, id).
		Scan(&sub.ID, &sub.ProblemID, &sub.Username, &sub.Language, &sub.Source, &sub.Status,
			&sub.Verdict, &sub.PassedCount, &cases, &sub.CompileLog, &created, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sub.CreatedAt = time.Unix(created, 0)
	if finished.Valid {
		t := time.Unix(finished.Int64, 0)
		sub.FinishedAt = &t
	}
	_ = jsonUnmarshal(cases, &sub.Cases)
	return &sub, nil
}

func (s *Store) SetSubmissionRunning(id int64) error {
	_, err := s.db.Exec(`UPDATE submissions SET status='running' WHERE id=?`, id)
	return err
}

func (s *Store) FinishSubmission(id int64, verdict string, passed int, cases []CaseResult) error {
	_, err := s.db.Exec(`UPDATE submissions SET status='done', verdict=?, passed_count=?,
		per_case_json=?, finished_at=? WHERE id=?`, verdict, passed, jsonMarshal(cases), unix(time.Now()), id)
	return err
}

func (s *Store) RequeueSubmission(id int64) error {
	_, err := s.db.Exec(`UPDATE submissions SET status='queued', verdict='', passed_count=0,
		per_case_json='[]', finished_at=NULL WHERE id=?`, id)
	return err
}

// RequeueInterrupted puts submissions left "running" by a crash or restart back
// in the queue, so nothing is silently stuck forever.
func (s *Store) RequeueInterrupted() (int64, error) {
	res, err := s.db.Exec(`UPDATE submissions SET status='queued', verdict='', passed_count=0,
		per_case_json='[]', finished_at=NULL WHERE status='running'`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) RequeueProblem(problemID int64) (int64, error) {
	res, err := s.db.Exec(`UPDATE submissions SET status='queued', verdict='', passed_count=0,
		per_case_json='[]', finished_at=NULL WHERE problem_id=?`, problemID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// NextQueued claims the oldest queued submission FIFO.
func (s *Store) NextQueued() (*Submission, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRow(`SELECT id FROM submissions WHERE status='queued' ORDER BY id LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE submissions SET status='running' WHERE id=?`, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.SubmissionByID(id)
}

type SubmissionRow struct {
	ID        int64
	ProblemID int64
	Slug      string
	Title     string
	Language  string
	Status    string
	Verdict   string
	Passed    int
	CreatedAt time.Time
}

func (s *Store) UserSubmissions(username string) ([]SubmissionRow, error) {
	rows, err := s.db.Query(`SELECT s.id, s.problem_id, p.slug, p.title, s.language, s.status,
		s.verdict, s.passed_count, s.created_at
		FROM submissions s JOIN problems p ON p.id=s.problem_id
		WHERE s.username=? ORDER BY s.id DESC LIMIT 100`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRows(rows)
}

func (s *Store) ProblemSubmissions(problemID int64) ([]SubmissionRow, error) {
	rows, err := s.db.Query(`SELECT s.id, s.problem_id, p.slug, p.title, s.language, s.status,
		s.verdict, s.passed_count, s.created_at
		FROM submissions s JOIN problems p ON p.id=s.problem_id
		WHERE s.problem_id=? ORDER BY s.id DESC LIMIT 200`, problemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRows(rows)
}

func scanRows(rows *sql.Rows) ([]SubmissionRow, error) {
	var out []SubmissionRow
	for rows.Next() {
		var r SubmissionRow
		var created int64
		if err := rows.Scan(&r.ID, &r.ProblemID, &r.Slug, &r.Title, &r.Language, &r.Status,
			&r.Verdict, &r.Passed, &created); err != nil {
			return nil, err
		}
		r.CreatedAt = time.Unix(created, 0)
		out = append(out, r)
	}
	return out, rows.Err()
}

type UserStats struct {
	Solved    []Problem
	Attempted int
	Subs      int
	ACSubs    int
}

func (s *Store) Stats(username string) (*UserStats, error) {
	st := &UserStats{}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM submissions WHERE username=?`, username).Scan(&st.Subs); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM submissions WHERE username=? AND verdict='AC' AND status='done'`,
		username).Scan(&st.ACSubs); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(DISTINCT problem_id) FROM submissions WHERE username=?`, username).
		Scan(&st.Attempted); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT DISTINCT p.id, p.slug, p.title FROM submissions s
		JOIN problems p ON p.id=s.problem_id
		WHERE s.username=? AND s.verdict='AC' AND s.status='done' ORDER BY p.title`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Problem
		if err := rows.Scan(&p.ID, &p.Slug, &p.Title); err != nil {
			return nil, err
		}
		st.Solved = append(st.Solved, p)
	}
	return st, rows.Err()
}

// ---- sessions ----

type Session struct {
	Token    string
	Username string
	IsAdmin  bool
	Expires  time.Time
}

func (s *Store) CreateSession(token, username string, isAdmin bool, ttl time.Duration) error {
	_, err := s.db.Exec(`INSERT INTO sessions (token, username, is_admin, expires_at) VALUES (?,?,?,?)`,
		token, username, boolInt(isAdmin), unix(time.Now().Add(ttl)))
	return err
}

func (s *Store) Session(token string) (*Session, error) {
	var ses Session
	var admin int
	var exp int64
	err := s.db.QueryRow(`SELECT token, username, is_admin, expires_at FROM sessions WHERE token=?`, token).
		Scan(&ses.Token, &ses.Username, &admin, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if time.Now().Unix() > exp {
		s.db.Exec(`DELETE FROM sessions WHERE token=?`, token)
		return nil, ErrNotFound
	}
	ses.IsAdmin = admin == 1
	ses.Expires = time.Unix(exp, 0)
	return &ses, nil
}

func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token=?`, token)
	return err
}

// ---- blocklist ----

type Blocked struct {
	Username  string
	Reason    string
	BlockedBy string
	BlockedAt time.Time
}

func (s *Store) Block(username, reason, by string) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO blocked_users (username, reason, blocked_by, blocked_at)
		VALUES (?,?,?,?)`, username, reason, by, unix(time.Now()))
	return err
}

func (s *Store) Unblock(username string) error {
	_, err := s.db.Exec(`DELETE FROM blocked_users WHERE username=?`, username)
	return err
}

func (s *Store) IsBlocked(username string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM blocked_users WHERE username=?`, username).Scan(&n)
	return n > 0, err
}

func (s *Store) BlockedUsers() ([]Blocked, error) {
	rows, err := s.db.Query(`SELECT username, reason, blocked_by, blocked_at FROM blocked_users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Blocked
	for rows.Next() {
		var b Blocked
		var at int64
		if err := rows.Scan(&b.Username, &b.Reason, &b.BlockedBy, &at); err != nil {
			return nil, err
		}
		b.BlockedAt = time.Unix(at, 0)
		out = append(out, b)
	}
	return out, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
