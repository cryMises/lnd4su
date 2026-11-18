package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/crymises/lnd4su/internal/judge"
	"github.com/crymises/lnd4su/internal/store"
)

type indexData struct {
	baseData
	Problems []store.Problem
}

func (a *App) index(w http.ResponseWriter, r *http.Request) {
	probs, err := a.St.ListProblems()
	if err != nil {
		serverError(w, err)
		return
	}
	counts, err := a.St.SolveCounts()
	if err != nil {
		serverError(w, err)
		return
	}
	for i := range probs {
		probs[i].SolvedCount = counts[probs[i].ID]
	}
	if s := a.session(r); s != nil {
		if err := a.St.MarkSolved(probs, s.Username); err != nil {
			serverError(w, err)
			return
		}
	}
	a.render(w, "index.html", indexData{baseData: a.base(r, "Daftar Soal"), Problems: probs})
}

type problemData struct {
	baseData
	P           *store.Problem
	Mine        []store.SubmissionRow
	PrevSource  string
	PrevLang    string
	BestVerdict string
	SolvedCount int
	Run         *judge.RunResult
	Stdin       string
}

func (a *App) problemPage(w http.ResponseWriter, r *http.Request) {
	p, err := a.St.ProblemBySlug(r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	d := problemData{baseData: a.base(r, p.Title), P: p}
	if counts, err := a.St.SolveCounts(); err == nil {
		d.SolvedCount = counts[p.ID]
	}
	if s := a.session(r); s != nil {
		rows, err := a.St.UserSubmissions(s.Username)
		if err != nil {
			serverError(w, err)
			return
		}
		for _, row := range rows {
			if row.ProblemID != p.ID {
				continue
			}
			d.Mine = append(d.Mine, row)
			if d.PrevSource == "" {
				if sub, err := a.St.SubmissionByID(row.ID); err == nil {
					d.PrevSource, d.PrevLang, d.BestVerdict = sub.Source, sub.Language, row.Verdict
				}
			}
		}
	}
	if d.PrevLang == "" {
		d.PrevLang = "c"
	}
	a.render(w, "problem.html", d)
}

func (a *App) statementPDF(w http.ResponseWriter, r *http.Request) {
	p, err := a.St.ProblemBySlug(r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	name, ok := a.St.StatementFile(p.ID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := openData(a.DataDir, "statements", name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "inline")
	http.ServeContent(w, r, name, p.CreatedAt, f)
}

type submissionData struct {
	baseData
	Sub      *store.Submission
	P        *store.Problem
	Subtasks []subtaskSummary
}

type subtaskSummary struct {
	Label string
	Pass  int
	Total int
}

func (a *App) submissionPage(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s := a.session(r)
	if s == nil {
		http.Redirect(w, r, "/login?next="+r.URL.Path, http.StatusSeeOther)
		return
	}
	sub, err := a.St.SubmissionByID(id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if sub.Username != s.Username && !s.IsAdmin {
		http.NotFound(w, r) // never leak someone else's code
		return
	}
	p, err := a.St.ProblemByID(sub.ProblemID)
	if err != nil {
		serverError(w, err)
		return
	}
	d := submissionData{baseData: a.base(r, fmt.Sprintf("Submission #%d", sub.ID)), Sub: sub, P: p}
	if cases, err := a.St.Cases(p.ID); err == nil {
		labels := map[int]string{}
		for _, c := range cases {
			labels[c.Position] = c.Subtask
		}
		type agg struct{ pass, total int }
		m := map[string]*agg{}
		var order []string
		for _, cr := range sub.Cases {
			l := labels[cr.Position]
			if l == "" {
				continue
			}
			if m[l] == nil {
				m[l] = &agg{}
				order = append(order, l)
			}
			m[l].total++
			if cr.Verdict == "AC" {
				m[l].pass++
			}
		}
		for _, l := range order {
			d.Subtasks = append(d.Subtasks, subtaskSummary{Label: l, Pass: m[l].pass, Total: m[l].total})
		}
	}
	a.render(w, "submission.html", d)
}

func (a *App) submissionStatus(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s := a.session(r)
	sub, err := a.St.SubmissionByID(id)
	if errors.Is(err, store.ErrNotFound) || (s == nil && err == nil) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if sub.Username != s.Username && !s.IsAdmin {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, map[string]any{
		"status":  sub.Status,
		"verdict": sub.Verdict,
		"passed":  sub.PassedCount,
	})
}

func (a *App) submit(w http.ResponseWriter, r *http.Request) {
	s := a.session(r)
	if s == nil {
		http.Error(w, "login diperlukan", http.StatusUnauthorized)
		return
	}
	p, err := a.St.ProblemBySlug(r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if !p.HasPDF {
		http.Error(w, "soal ini belum punya PDF, belum bisa disubmit", http.StatusBadRequest)
		return
	}
	blocked, err := a.St.IsBlocked(s.Username)
	if err != nil {
		serverError(w, err)
		return
	}
	if blocked {
		http.Error(w, "akun ini diblokir dari penilaian", http.StatusForbidden)
		return
	}
	src := r.FormValue("source")
	lang := r.FormValue("language")
	if lang != "cpp" {
		lang = "c"
	}
	if strings.TrimSpace(src) == "" {
		http.Error(w, "kode masih kosong", http.StatusBadRequest)
		return
	}
	if len(src) > judge.SrcCapBytes {
		http.Error(w, "kode melebihi 64 KB", http.StatusBadRequest)
		return
	}
	id, err := a.St.CreateSubmission(p.ID, s.Username, lang, src)
	if err != nil {
		serverError(w, err)
		return
	}
	a.Judge.Submit()
	http.Redirect(w, r, fmt.Sprintf("/s/%d", id), http.StatusSeeOther)
}

type runData struct {
	baseData
	P      *store.Problem
	Source string
	Lang   string
	Stdin  string
	Result *judge.RunResult
}

func (a *App) runMode(w http.ResponseWriter, r *http.Request) {
	s := a.session(r)
	if s == nil {
		http.Error(w, "login diperlukan", http.StatusUnauthorized)
		return
	}
	p, err := a.St.ProblemBySlug(r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	blocked, err := a.St.IsBlocked(s.Username)
	if err != nil {
		serverError(w, err)
		return
	}
	if blocked {
		http.Error(w, "akun ini diblokir dari penilaian", http.StatusForbidden)
		return
	}
	src := r.FormValue("source")
	lang := r.FormValue("language")
	if lang != "cpp" {
		lang = "c"
	}
	stdin := r.FormValue("stdin")
	d := runData{baseData: a.base(r, "Coba Kode — "+p.Title), P: p, Source: src, Lang: lang, Stdin: stdin}
	if strings.TrimSpace(src) == "" {
		d.Error = "kode masih kosong"
		a.render(w, "problem.html", problemData{baseData: d.baseData, P: p, PrevSource: src, PrevLang: lang, Stdin: stdin})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	res, err := a.Judge.RunCustom(ctx, lang, src, stdin, p.TimeLimitMS, int64(p.MemoryLimitKB))
	if err != nil {
		d.Error = "gagal menjalankan: " + err.Error()
	} else {
		d.Result = res
	}
	a.render(w, "problem.html", problemData{baseData: d.baseData, P: p, PrevSource: src, PrevLang: lang, Stdin: stdin, Run: d.Result})
}

type loginData struct {
	baseData
	Next string
}

func (a *App) loginForm(w http.ResponseWriter, r *http.Request) {
	if a.session(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	a.render(w, "login.html", loginData{baseData: a.base(r, "Masuk"), Next: r.URL.Query().Get("next")})
}

func (a *App) loginPost(w http.ResponseWriter, r *http.Request) {
	login := strings.TrimSpace(r.FormValue("username"))
	pass := r.FormValue("password")
	next := r.FormValue("next")
	if next == "" || !strings.HasPrefix(next, "/") {
		next = "/"
	}
	key := login + "|" + clientIP(r)
	if a.Lockout.Blocked(key) {
		a.render(w, "login.html", loginData{baseData: baseData{Title: "Masuk", WikiURL: a.WikiURL,
			Error: "terlalu banyak percobaan, coba lagi 5 menit lagi"}, Next: next})
		return
	}
	entry, err := a.Users.Lookup(login)
	if err != nil || entry == nil || !authVerify(entry, pass) {
		a.Lockout.Fail(key)
		a.render(w, "login.html", loginData{baseData: baseData{Title: "Masuk", WikiURL: a.WikiURL,
			Error: "nama pengguna atau kata sandi salah"}, Next: next})
		return
	}
	a.Lockout.Reset(key)
	token := newToken()
	const ttl = 7 * 24 * time.Hour
	if err := a.St.CreateSession(token, entry.Login, isAdmin(entry), ttl); err != nil {
		serverError(w, err)
		return
	}
	a.setSession(w, token, ttl)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("lnd4su"); err == nil {
		_ = a.St.DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "lnd4su", Value: "", Path: "/", HttpOnly: true,
		Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

type meData struct {
	baseData
	Stats *store.UserStats
	Subs  []store.SubmissionRow
}

func (a *App) me(w http.ResponseWriter, r *http.Request) {
	s := a.session(r)
	if s == nil {
		http.Redirect(w, r, "/login?next=/me", http.StatusSeeOther)
		return
	}
	st, err := a.St.Stats(s.Username)
	if err != nil {
		serverError(w, err)
		return
	}
	subs, err := a.St.UserSubmissions(s.Username)
	if err != nil {
		serverError(w, err)
		return
	}
	a.render(w, "me.html", meData{baseData: a.base(r, "Akun Saya"), Stats: st, Subs: subs})
}

func serverError(w http.ResponseWriter, err error) {
	http.Error(w, "kesalahan server: "+err.Error(), http.StatusInternalServerError)
}

// ---- problem bundle (export/import) ----

type bundleCase struct {
	Position int    `json:"position"`
	Stdin    string `json:"stdin"`
	Expected string `json:"expected_stdout"`
	Subtask  string `json:"subtask"`
}

type bundle struct {
	Slug          string         `json:"slug"`
	Title         string         `json:"title"`
	Samples       []store.Sample `json:"samples"`
	Tags          string         `json:"tags"`
	Difficulty    string         `json:"difficulty"`
	TimeLimitMS   int            `json:"time_limit_ms"`
	MemoryLimitKB int            `json:"memory_limit_kb"`
	CompareMode   string         `json:"compare_mode"`
	WikiPageID    string         `json:"wiki_page_id"`
	Cases         []bundleCase   `json:"test_cases"`
}

func bundleOf(p *store.Problem, cases []store.TestCase) bundle {
	b := bundle{Slug: p.Slug, Title: p.Title, Samples: p.Samples, Tags: p.Tags,
		Difficulty: p.Difficulty, TimeLimitMS: p.TimeLimitMS, MemoryLimitKB: p.MemoryLimitKB,
		CompareMode: p.CompareMode, WikiPageID: p.WikiPageID}
	for _, c := range cases {
		b.Cases = append(b.Cases, bundleCase{Position: c.Position, Stdin: c.Stdin,
			Expected: c.Expected, Subtask: c.Subtask})
	}
	return b
}

func (b *bundle) validate() error {
	if strings.TrimSpace(b.Title) == "" {
		return errors.New("judul wajib diisi")
	}
	if strings.TrimSpace(b.Slug) == "" {
		return errors.New("slug wajib diisi")
	}
	if len(b.Cases) != 10 {
		return fmt.Errorf("harus tepat 10 test case, dapat %d", len(b.Cases))
	}
	// Defaults must land on the caller's bundle (pointer receiver): a bare JSON
	// import carries no limits, and 0 ms would fail every case as TLE.
	if b.TimeLimitMS <= 0 {
		b.TimeLimitMS = 1000
	}
	if b.MemoryLimitKB <= 0 {
		b.MemoryLimitKB = 65536
	}
	if b.CompareMode != "exact" {
		b.CompareMode = "tolerant"
	}
	if b.Difficulty == "" {
		b.Difficulty = "Mudah"
	}
	return nil
}

func (a *App) adminExport(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, err := a.St.ProblemByID(id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	cases, err := a.St.Cases(p.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	jb, _ := json.MarshalIndent(bundleOf(p, cases), "", "  ")
	fw, err := zw.Create("problem.json")
	if err != nil {
		serverError(w, err)
		return
	}
	fw.Write(jb)
	if name, ok := a.St.StatementFile(p.ID); ok {
		if data, err := readData(a.DataDir, "statements", name); err == nil {
			if pw, err := zw.Create("statement.pdf"); err == nil {
				pw.Write(data)
			}
		}
	}
	zw.Close()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", p.Slug+".zip"))
	w.Write(buf.Bytes())
}

var (
	authVerify = verifyPassword
	isAdmin    = adminFlag
	newToken   = newSessionToken
)

func (a *App) adminImport(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		adminErr(w, "gagal membaca unggahan: "+err.Error())
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		adminErr(w, "tidak ada file terpilih")
		return
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 8<<20))
	if err != nil {
		adminErr(w, "gagal membaca file")
		return
	}
	var jsonBytes, pdfBytes []byte
	switch {
	case strings.HasSuffix(strings.ToLower(hdr.Filename), ".zip"):
		zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			adminErr(w, "ZIP tidak valid")
			return
		}
		for _, zf := range zr.File {
			rc, err := zf.Open()
			if err != nil {
				adminErr(w, "ZIP rusak")
				return
			}
			data, _ := io.ReadAll(io.LimitReader(rc, 8<<20))
			rc.Close()
			switch zf.Name {
			case "problem.json":
				jsonBytes = data
			case "statement.pdf":
				pdfBytes = data
			}
		}
	default:
		jsonBytes = raw
	}
	var b bundle
	if err := json.Unmarshal(jsonBytes, &b); err != nil {
		adminErr(w, "JSON tidak valid: "+err.Error())
		return
	}
	if err := b.validate(); err != nil {
		adminErr(w, err.Error())
		return
	}
	p := &store.Problem{Slug: b.Slug, Title: b.Title, Samples: b.Samples, Tags: b.Tags,
		Difficulty: b.Difficulty, TimeLimitMS: b.TimeLimitMS, MemoryLimitKB: b.MemoryLimitKB,
		CompareMode: b.CompareMode, WikiPageID: b.WikiPageID, CreatedBy: a.session(r).Username}
	cases := make([]store.TestCase, 0, 10)
	for i, c := range b.Cases {
		cases = append(cases, store.TestCase{Position: i + 1, Stdin: c.Stdin, Expected: c.Expected, Subtask: c.Subtask})
	}
	id, err := a.St.CreateProblem(p, cases)
	if err != nil {
		adminErr(w, "gagal impor: "+err.Error())
		return
	}
	if len(pdfBytes) > 0 && bytes.HasPrefix(pdfBytes, []byte("%PDF")) {
		name := fmt.Sprintf("%d.pdf", id)
		if err := writeData(a.DataDir, []string{"statements", name}, pdfBytes); err != nil {
			adminErr(w, "impor berhasil tapi PDF gagal disimpan: "+err.Error())
			return
		}
		_ = a.St.SetStatement(id, name)
	}
	http.Redirect(w, r, "/admin/problems", http.StatusSeeOther)
}

func adminErr(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	io.WriteString(w, "Gagal: "+msg+"\n\nKembali dan perbaiki berkasnya.")
}
