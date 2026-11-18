package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crymises/lnd4su/internal/auth"
	"github.com/crymises/lnd4su/internal/store"
	"golang.org/x/crypto/bcrypt"
)

// The login boundary is the whole security rule: browsing public, execution
// only for a verified wiki account. This drives it end to end with a real
// DokuWiki-shaped users.auth.php ($2y$ prefix included).
func TestLoginAndSubmitGate(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()

	hash, err := bcrypt.GenerateFromPassword([]byte("rahasia123"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	dokuwiki := "$2y$" + string(hash[4:]) // DokuWiki/PHP writes $2y$
	users := filepath.Join(dir, "users.auth.php")
	body := "# users\n" + "guru:" + dokuwiki + ":Guru:guru@example.test:admin,user\n" +
		"siswa:" + dokuwiki + ":Siswa:siswa@example.test:user\n"
	if err := os.WriteFile(users, []byte(body), 0o644); err != nil {
		t.Fatalf("users: %v", err)
	}

	a := &App{St: st, Users: auth.UsersFile{Path: users}, Lockout: auth.NewLockout(),
		DataDir: dir, WikiURL: "https://wiki.test"}
	srv := httptest.NewServer(a.Routes())
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	post := func(path string, form url.Values, cookie *http.Cookie) *http.Response {
		req, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		resp.Body.Close()
		return resp
	}
	get := func(path string, cookie *http.Cookie) (int, string) {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// Wrong password: rejected, no cookie.
	if resp := post("/login", url.Values{"username": {"guru"}, "password": {"salah"}}, nil); len(resp.Cookies()) != 0 {
		t.Fatal("wrong password handed out a session cookie")
	}

	// Correct password: session cookie, /me and /admin work.
	resp := post("/login", url.Values{"username": {"guru"}, "password": {"rahasia123"}}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d, want 303", resp.StatusCode)
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "lnd4su" {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || !cookie.Secure {
		t.Fatalf("session cookie missing or not HttpOnly+Secure: %+v", cookie)
	}
	if code, _ := get("/me", cookie); code != 200 {
		t.Errorf("/me as guru: %d", code)
	}
	if code, _ := get("/admin/problems", cookie); code != 200 {
		t.Errorf("/admin/problems as admin: %d", code)
	}

	// A logged-in visitor gets the real submit + Run forms (the template reads
	// fields only present in that branch).
	p := &store.Problem{Slug: "t2", Title: "Soal Uji", Difficulty: "Mudah",
		TimeLimitMS: 1000, MemoryLimitKB: 65536, CompareMode: "tolerant"}
	if id, err := st.CreateProblem(p, tenCases()); err != nil {
		t.Fatalf("create problem: %v", err)
	} else {
		p.ID = id
	}
	if err := st.SetStatement(p.ID, "1.pdf"); err != nil {
		t.Fatalf("set statement: %v", err)
	}
	if code, body := get("/p/t2", cookie); code != 200 || !strings.Contains(body, `name="stdin"`) {
		t.Errorf("logged-in problem page: %d, run form missing (len %d)", code, len(body))
	}

	// A plain wiki user is not an admin.
	resp = post("/login", url.Values{"username": {"siswa"}, "password": {"rahasia123"}}, nil)
	var userCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "lnd4su" {
			userCookie = c
		}
	}
	if userCookie == nil {
		t.Fatal("siswa login produced no cookie")
	}
	if code, _ := get("/admin/problems", userCookie); code != http.StatusForbidden {
		t.Errorf("non-admin /admin/problems: %d, want 403", code)
	}

	// Execution gate: anonymous submit and Run are rejected outright.
	if code := post("/p/t1/submit", url.Values{"language": {"c"}, "source": {"int main(){return 0;}"}}, nil).StatusCode; code != http.StatusUnauthorized {
		t.Errorf("anonymous submit: %d, want 401", code)
	}
	if code := post("/p/t1/run", url.Values{"language": {"c"}, "source": {"int main(){return 0;}"}}, nil).StatusCode; code != http.StatusUnauthorized {
		t.Errorf("anonymous run: %d, want 401", code)
	}

	// Logout kills the session.
	if code := post("/logout", url.Values{}, cookie).StatusCode; code != http.StatusSeeOther {
		t.Errorf("logout: %d", code)
	}
	if code, _ := get("/me", cookie); code == 200 {
		t.Error("/me still works after logout")
	}

	// /s/<id> is owner+admin only, and the submission page must render with a
	// finished verdict: per-case detail is the point of the page.
	subID, err := st.CreateSubmission(p.ID, "guru", "c", "int main(){return 0;}")
	if err != nil {
		t.Fatalf("create submission: %v", err)
	}
	cases := tenCases()
	cases[1].Expected = "3\n"
	results := make([]store.CaseResult, 0, 10)
	for _, c := range cases {
		r := store.CaseResult{Position: c.Position, Verdict: "AC", TimeMS: 3, MemoryKB: 1024}
		if c.Position == 2 {
			r.Verdict = "WA"
			r.Stdin, r.Expected, r.Actual = c.Stdin, c.Expected, "2\n"
		}
		results = append(results, r)
	}
	if err := st.FinishSubmission(subID, "WA", 9, results); err != nil {
		t.Fatalf("finish submission: %v", err)
	}
	if err := st.CreateSession("testsession1", "guru", true, time.Hour); err != nil {
		t.Fatalf("session: %v", err)
	}
	adminCookie := &http.Cookie{Name: "lnd4su", Value: "testsession1"}
	if code, body := get("/s/1", adminCookie); code != 200 || !strings.Contains(body, "WA") || !strings.Contains(body, "9/10") {
		t.Errorf("submission page: %d, verdict/score missing", code)
	}

	// Admin surfaces render with real data: list, new-problem form (10 blank
	// case blocks), edit form, and the user page.
	for _, path := range []string{"/admin/problems", "/admin/problems/new", "/admin/users", "/admin/problems/1/edit"} {
		code, body := get(path, adminCookie)
		if code != 200 {
			t.Errorf("%s: %d, want 200", path, code)
			continue
		}
		if !strings.Contains(body, "LND4SU") {
			t.Errorf("%s: rendered without the header", path)
		}
	}
	if _, body := get("/admin/problems/new", adminCookie); !strings.Contains(body, "case_10_stdin") {
		t.Error("new-problem form is missing the 10th test case block")
	}
}
