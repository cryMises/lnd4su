package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/crymises/lnd4su/internal/store"
)

type adminHomeData struct {
	baseData
	Problems []store.Problem
	Blocked  []store.Blocked
}

func (a *App) adminIndex(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}
	http.Redirect(w, r, "/admin/problems", http.StatusSeeOther)
}

func (a *App) adminProblems(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}
	probs, err := a.St.ListProblems()
	if err != nil {
		serverError(w, err)
		return
	}
	blocked, err := a.St.BlockedUsers()
	if err != nil {
		serverError(w, err)
		return
	}
	a.render(w, "admin.html", adminHomeData{baseData: a.base(r, "Admin"), Problems: probs, Blocked: blocked})
}

// --- problem form (create + edit share one template) ---

type formCase struct {
	Position int
	Stdin    string
	Expected string
	Subtask  string
}

type problemFormData struct {
	baseData
	P        *store.Problem
	IsNew    bool
	Cases    []formCase
	Saved    bool
	RawSlug  string
	RawTitle string
}

func (a *App) adminProblemForm(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}
	if r.PathValue("id") == "" {
		cases := make([]formCase, 10)
		for i := range cases {
			cases[i].Position = i + 1
		}
		a.render(w, "problem_form.html", problemFormData{
			baseData: a.base(r, "Soal Baru"), IsNew: true,
			P: &store.Problem{TimeLimitMS: 1000, MemoryLimitKB: 65536,
				CompareMode: "tolerant", Difficulty: "Mudah"}, Cases: cases,
		})
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
	cs, err := a.St.Cases(id)
	if err != nil {
		serverError(w, err)
		return
	}
	cases := make([]formCase, 0, 10)
	for _, c := range cs {
		cases = append(cases, formCase{Position: c.Position, Stdin: c.Stdin, Expected: c.Expected, Subtask: c.Subtask})
	}
	a.render(w, "problem_form.html", problemFormData{baseData: a.base(r, "Ubah Soal"), P: p, Cases: cases})
}

// formToProblem parses the shared problem form. Returns nil + message on bad
// input (the message is user-facing Indonesian).
func formToProblem(r *http.Request) (*store.Problem, []store.TestCase, bool, string) {
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		return nil, nil, false, "gagal membaca formulir"
	}
	p := &store.Problem{
		Slug:        strings.TrimSpace(r.FormValue("slug")),
		Title:       strings.TrimSpace(r.FormValue("title")),
		Tags:        strings.TrimSpace(r.FormValue("tags")),
		Difficulty:  r.FormValue("difficulty"),
		CompareMode: r.FormValue("compare_mode"),
		WikiPageID:  strings.TrimSpace(r.FormValue("wiki_page_id")),
	}
	p.TimeLimitMS, _ = strconv.Atoi(r.FormValue("time_limit_ms"))
	p.MemoryLimitKB, _ = strconv.Atoi(r.FormValue("memory_limit_kb"))
	if p.TimeLimitMS <= 0 {
		p.TimeLimitMS = 1000
	}
	if p.MemoryLimitKB <= 0 {
		p.MemoryLimitKB = 65536
	}
	if p.Difficulty == "" {
		p.Difficulty = "Mudah"
	}
	if p.CompareMode != "exact" {
		p.CompareMode = "tolerant"
	}
	if p.Slug == "" || p.Title == "" {
		return nil, nil, false, "judul dan slug wajib diisi"
	}
	for _, ch := range p.Slug {
		if !(ch == '-' || ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')) {
			return nil, nil, false, "slug hanya huruf kecil, angka, - dan _"
		}
	}
	// samples: textarea "input | output" per line
	for _, line := range strings.Split(r.FormValue("samples"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		in, out, ok := strings.Cut(line, "|")
		if !ok {
			return nil, nil, false, "sampel harus berbentuk: input | output (satu per baris)"
		}
		p.Samples = append(p.Samples, store.Sample{Input: strings.TrimSpace(in), Output: strings.TrimSpace(out)})
	}
	cases := make([]store.TestCase, 0, 10)
	for i := 1; i <= 10; i++ {
		cases = append(cases, store.TestCase{
			Position: i,
			Stdin:    r.FormValue(fmt.Sprintf("case_%d_stdin", i)),
			Expected: r.FormValue(fmt.Sprintf("case_%d_expected", i)),
			Subtask:  strings.TrimSpace(r.FormValue(fmt.Sprintf("case_%d_subtask", i))),
		})
	}
	return p, cases, true, ""
}

// formSlug etc. keep the rendered values after a validation error.
func formEcho(p *store.Problem) (slug, title string) {
	if p == nil {
		return "", ""
	}
	return p.Slug, p.Title
}

func (a *App) adminProblemCreate(w http.ResponseWriter, r *http.Request) {
	s := a.requireAdmin(w, r)
	if s == nil {
		return
	}
	p, cases, ok, msg := formToProblem(r)
	if !ok {
		adminErr(w, msg)
		return
	}
	p.CreatedBy = s.Username
	id, err := a.St.CreateProblem(p, cases)
	if err != nil {
		adminErr(w, "gagal menyimpan: "+err.Error())
		return
	}
	if msg := a.saveUploadedPDF(r, id); msg != "" {
		adminErr(w, msg)
		return
	}
	http.Redirect(w, r, "/admin/problems", http.StatusSeeOther)
}

func (a *App) adminProblemEdit(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	old, err := a.St.ProblemByID(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, cases, ok, msg := formToProblem(r)
	if !ok {
		adminErr(w, msg)
		return
	}
	p.ID, p.Slug = id, old.Slug // slug is the stable URL; not editable
	p.CreatedBy, p.CreatedAt = old.CreatedBy, old.CreatedAt
	if err := a.St.UpdateProblem(p, cases); err != nil {
		adminErr(w, "gagal menyimpan: "+err.Error())
		return
	}
	if msg := a.saveUploadedPDF(r, id); msg != "" {
		adminErr(w, msg)
		return
	}
	http.Redirect(w, r, "/admin/problems", http.StatusSeeOther)
}

// saveUploadedPDF stores the multipart statement when present. Returns a
// user-facing error message or "".
func (a *App) saveUploadedPDF(r *http.Request, id int64) string {
	f, _, err := r.FormFile("statement")
	if err != nil {
		return "" // no upload is fine
	}
	defer f.Close()
	head := make([]byte, 5)
	n, _ := f.Read(head)
	if !bytes.HasPrefix(head[:n], []byte("%PDF")) {
		return "berkas pernyataan harus PDF (diawali %PDF)"
	}
	rest, err := readAllLimit(f, 5<<20)
	if err != nil {
		return "PDF melebihi 5 MB"
	}
	name := fmt.Sprintf("%d.pdf", id)
	if err := writeData(a.DataDir, []string{"statements", name}, append(head[:n], rest...)); err != nil {
		return "gagal menyimpan PDF: " + err.Error()
	}
	if err := a.St.SetStatement(id, name); err != nil {
		return "gagal menautkan PDF: " + err.Error()
	}
	// Re-render the statement pages from the new PDF. Best effort: a
	// failure only logs, the problem page regenerates them lazily.
	a.dropStatementPages(id)
	if _, err := a.statementPages(id, name); err != nil {
		log.Printf("statement pages %d: %v", id, err)
	}
	return ""
}

func (a *App) adminProblemPDF(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if msg := a.saveUploadedPDF(r, id); msg != "" {
		adminErr(w, msg)
		return
	}
	http.Redirect(w, r, "/admin/problems", http.StatusSeeOther)
}

func (a *App) adminProblemDelete(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if name, ok := a.St.StatementFile(id); ok {
		removeData(a.DataDir, "statements", name)
	}
	a.dropStatementPages(id)
	if err := a.St.DeleteProblem(id); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/problems", http.StatusSeeOther)
}

func (a *App) adminRejudge(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	sub, err := a.St.SubmissionByID(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := a.St.RequeueSubmission(id); err != nil {
		serverError(w, err)
		return
	}
	a.Judge.Submit()
	http.Redirect(w, r, fmt.Sprintf("/s/%d", sub.ID), http.StatusSeeOther)
}

func (a *App) adminRejudgeProblem(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := a.St.RequeueProblem(id); err != nil {
		serverError(w, err)
		return
	}
	a.Judge.Submit()
	http.Redirect(w, r, "/admin/problems", http.StatusSeeOther)
}

type usersData struct {
	baseData
	Blocked []store.Blocked
	Rows    []store.SubmissionRow
}

func (a *App) adminUsers(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}
	blocked, err := a.St.BlockedUsers()
	if err != nil {
		serverError(w, err)
		return
	}
	a.render(w, "admin.html", adminHomeData{baseData: a.base(r, "Admin — Pengguna"),
		Blocked: blocked})
}

func (a *App) adminBlock(w http.ResponseWriter, r *http.Request) {
	s := a.requireAdmin(w, r)
	if s == nil {
		return
	}
	user := strings.TrimSpace(r.FormValue("username"))
	if user == "" {
		adminErr(w, "nama pengguna kosong")
		return
	}
	if err := a.St.Block(user, strings.TrimSpace(r.FormValue("reason")), s.Username); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/problems", http.StatusSeeOther)
}

func (a *App) adminUnblock(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}
	if err := a.St.Unblock(strings.TrimSpace(r.FormValue("username"))); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/problems", http.StatusSeeOther)
}
