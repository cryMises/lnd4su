package handlers

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/crymises/lnd4su/internal/auth"
	"github.com/crymises/lnd4su/internal/judge"
	"github.com/crymises/lnd4su/internal/store"
)

//go:embed all:templates
var templateFS embed.FS

// App wires stores, judge and templates into HTTP handlers.
type App struct {
	St      *store.Store
	Users   auth.UsersFile
	Lockout *auth.Lockout
	Judge   *judge.Judge
	DataDir string
	WikiURL string

	tmpl     *template.Template
	tmplOnce sync.Once
	tmplErr  error
}

type baseData struct {
	Title   string
	Session *store.Session
	Flash   string
	Error   string
	WikiURL string
	Page    string
}

func (a *App) loadTemplates() error {
	a.tmplOnce.Do(func() {
		a.tmplErr = a.parseTemplates()
	})
	return a.tmplErr
}

func (a *App) parseTemplates() error {
	t, err := template.New("").Funcs(template.FuncMap{
		"eq":           func(a, b any) bool { return fmt.Sprint(a) == fmt.Sprint(b) },
		"fmtTime":      func(t time.Time) string { return t.Format("2 Jan 2006 15:04") },
		"verdictClass": func(v string) string { return "v-" + strings.ToLower(v) },
		"wikiPageURL":  func(id string) string { return a.WikiURL + "/doku.php?id=" + id },
	}).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return err
	}
	a.tmpl = t
	return nil
}

func (a *App) render(w http.ResponseWriter, page string, data any) {
	if err := a.loadTemplates(); err != nil {
		log.Printf("templates: %v", err)
		http.Error(w, "internal error", 500)
		return
	}
	buf := &strings.Builder{}
	if err := a.tmpl.ExecuteTemplate(buf, page, data); err != nil {
		log.Printf("render %s: %v", page, err)
		http.Error(w, "internal error", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, buf.String())
}

// base builds the fields every page template gets.
func (a *App) base(r *http.Request, title string) baseData {
	return baseData{Title: title, Session: a.session(r), WikiURL: a.WikiURL}
}

func (a *App) session(r *http.Request) *store.Session {
	c, err := r.Cookie("lnd4su")
	if err != nil {
		return nil
	}
	s, err := a.St.Session(c.Value)
	if err != nil {
		return nil
	}
	return s
}

func (a *App) setSession(w http.ResponseWriter, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     "lnd4su",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(ttl),
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// requireLogin writes the login prompt and returns false when anonymous.
func (a *App) requireLogin(w http.ResponseWriter, r *http.Request) bool {
	if a.session(r) == nil {
		if r.Method == http.MethodGet {
			http.Redirect(w, r, "/login?next="+r.URL.Path, http.StatusSeeOther)
		} else {
			http.Error(w, "login diperlukan", http.StatusUnauthorized)
		}
		return false
	}
	return true
}

func (a *App) requireAdmin(w http.ResponseWriter, r *http.Request) *store.Session {
	s := a.session(r)
	if s == nil {
		http.Redirect(w, r, "/login?next="+r.URL.Path, http.StatusSeeOther)
		return nil
	}
	if !s.IsAdmin {
		http.Error(w, "khusus admin", http.StatusForbidden)
		return nil
	}
	return s
}

// Routes builds the mux.
func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	})
	mux.HandleFunc("GET /{$}", a.index)
	mux.HandleFunc("GET /p/{slug}", a.problemPage)
	mux.HandleFunc("GET /p/{slug}/statement.pdf", a.statementPDF)
	mux.HandleFunc("GET /p/{slug}/pages/{file}", a.statementPageImage)
	mux.HandleFunc("POST /p/{slug}/submit", a.submit)
	mux.HandleFunc("POST /p/{slug}/run", a.runMode)
	mux.HandleFunc("GET /s/{id}", a.submissionPage)
	mux.HandleFunc("GET /s/{id}/status", a.submissionStatus)
	mux.HandleFunc("GET /login", a.loginForm)
	mux.HandleFunc("POST /login", a.loginPost)
	mux.HandleFunc("POST /logout", a.logout)
	mux.HandleFunc("GET /me", a.me)
	mux.HandleFunc("GET /admin", a.adminIndex)
	mux.HandleFunc("GET /admin/problems", a.adminProblems)
	mux.HandleFunc("GET /admin/problems/new", a.adminProblemForm)
	mux.HandleFunc("POST /admin/problems/new", a.adminProblemCreate)
	mux.HandleFunc("GET /admin/problems/{id}/edit", a.adminProblemForm)
	mux.HandleFunc("POST /admin/problems/{id}/edit", a.adminProblemEdit)
	mux.HandleFunc("POST /admin/problems/{id}/delete", a.adminProblemDelete)
	mux.HandleFunc("POST /admin/problems/{id}/pdf", a.adminProblemPDF)
	mux.HandleFunc("GET /admin/problems/{id}/export", a.adminExport)
	mux.HandleFunc("POST /admin/problems/import", a.adminImport)
	mux.HandleFunc("POST /admin/submissions/{id}/rejudge", a.adminRejudge)
	mux.HandleFunc("POST /admin/problems/{id}/rejudge", a.adminRejudgeProblem)
	mux.HandleFunc("GET /admin/users", a.adminUsers)
	mux.HandleFunc("POST /admin/users/block", a.adminBlock)
	mux.HandleFunc("POST /admin/users/unblock", a.adminUnblock)
	mux.Handle("GET /static/", http.FileServer(http.FS(staticFS)))
	// The manifest and the service worker must be served from the root: /sw.js
	// has to be at the root or its scope would not cover the app.
	mux.HandleFunc("GET /manifest.json", func(w http.ResponseWriter, r *http.Request) {
		serveEmbedded(w, "static/manifest.json", "application/manifest+json")
	})
	mux.HandleFunc("GET /sw.js", func(w http.ResponseWriter, r *http.Request) {
		// no-store so a redeploy actually reaches the browser.
		w.Header().Set("Cache-Control", "no-store")
		serveEmbedded(w, "static/sw.js", "application/javascript")
	})
	return logRequests(mux)
}

// serveEmbedded writes one file straight out of staticFS with a fixed type.
func serveEmbedded(w http.ResponseWriter, name, ctype string) {
	b, err := staticFS.ReadFile(name)
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Write(b)
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h.ServeHTTP(w, r)
		log.Printf("%s %s (%s)", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func parseID(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) }

var errBadInput = errors.New("bad input")
