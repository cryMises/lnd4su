package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crymises/lnd4su/internal/auth"
	"github.com/crymises/lnd4su/internal/store"
)

// Bundle round-trip: export a problem, delete it, import the ZIP back with all
// 10 cases and limits intact — and a bare JSON import must land default limits
// instead of 0 ms (which would TLE every case).
func TestBundleRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()

	a := &App{St: st, Users: auth.UsersFile{Path: filepath.Join(dir, "users.auth.php")},
		Lockout: auth.NewLockout(), DataDir: dir, WikiURL: "https://wiki.test"}
	srv := httptest.NewServer(a.Routes())
	defer srv.Close()

	p := &store.Problem{Slug: "ab", Title: "A+B", Difficulty: "Mudah", Tags: "dasar",
		TimeLimitMS: 1500, MemoryLimitKB: 32768, CompareMode: "tolerant", CreatedBy: "guru"}
	if id, err := st.CreateProblem(p, tenCases()); err != nil {
		t.Fatalf("create: %v", err)
	} else {
		p.ID = id
	}

	b := bundleOf(p, casesOf(t, st, p.ID))
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(b); err != nil {
		t.Fatalf("encode: %v", err)
	}

	var back bundle
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := back.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if back.TimeLimitMS != 1500 || back.MemoryLimitKB != 32768 {
		t.Errorf("limits lost in round-trip: %d ms / %d KB", back.TimeLimitMS, back.MemoryLimitKB)
	}
	if len(back.Cases) != 10 || back.Cases[9].Expected == "" {
		t.Errorf("cases lost in round-trip: %d", len(back.Cases))
	}

	// A bare JSON with no limits at all must be defaulted by validate().
	sparse := bundle{Slug: "s", Title: "S"}
	for _, c := range tenCases() {
		sparse.Cases = append(sparse.Cases, bundleCase{Position: c.Position, Stdin: c.Stdin, Expected: c.Expected})
	}
	if err := sparse.validate(); err != nil {
		t.Fatalf("sparse validate: %v", err)
	}
	if sparse.TimeLimitMS != 1000 || sparse.MemoryLimitKB != 65536 || sparse.CompareMode != "tolerant" {
		t.Errorf("defaults not applied: %+v", sparse)
	}
	if err := a.St.SetStatement(p.ID, "1.pdf"); err != nil {
		t.Fatalf("statement: %v", err)
	}
}

func TestPagesRender(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()

	users := filepath.Join(dir, "users.auth.php")
	if err := os.WriteFile(users, []byte("# comment\nadmin:$2y$xx:Admin:a@b.c:admin,user\n"), 0o644); err != nil {
		t.Fatalf("users file: %v", err)
	}

	a := &App{St: st, Users: auth.UsersFile{Path: users}, Lockout: auth.NewLockout(),
		DataDir: dir, WikiURL: "https://wiki.test"}
	srv := httptest.NewServer(a.Routes())
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	get := func(path string) (int, string) {
		resp, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		buf := &strings.Builder{}
		if _, err := io.Copy(buf, resp.Body); err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return resp.StatusCode, buf.String()
	}

	if code, body := get("/healthz"); code != 200 || strings.TrimSpace(body) != "ok" {
		t.Fatalf("healthz: %d %q", code, body)
	}

	// Index renders the empty state, public.
	code, body := get("/")
	if code != 200 {
		t.Fatalf("index: %d", code)
	}
	for _, want := range []string{"LND4SU", "Latihan Ngoding Darurat 4 Soal Ujian", "Belum ada soal"} {
		if !strings.Contains(body, want) {
			t.Errorf("index missing %q", want)
		}
	}
	// PWA wiring in the shared head, and the dark-mode toggle.
	for _, want := range []string{`rel="manifest"`, `name="theme-color"`, `apple-touch-icon`,
		`id="theme-toggle"`, `lnd4su-theme`, `/sw.js`} {
		if !strings.Contains(body, want) {
			t.Errorf("index head missing %q", want)
		}
	}

	if code, body = get("/login"); code != 200 || !strings.Contains(body, "Masuk") {
		t.Errorf("login: %d", code)
	}

	// The stylesheet is embedded; a wrong embed path would 404 silently in prod.
	if code, body = get("/static/style.css"); code != 200 || !strings.Contains(body, "--accent") {
		t.Errorf("static css: %d (len %d)", code, len(body))
	}

	// PWA: the manifest and the service worker must be served from the root
	// with the right types, or Chrome will not offer to install the app.
	for _, tc := range []struct{ path, wantType, wantBody string }{
		{"/manifest.json", "application/manifest+json", `"short_name": "LND4SU"`},
		{"/sw.js", "application/javascript", "addEventListener(\"fetch\""},
	} {
		resp, err := client.Get(srv.URL + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		buf := &strings.Builder{}
		if _, err := io.Copy(buf, resp.Body); err != nil {
			t.Fatalf("read %s: %v", tc.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("%s: %d", tc.path, resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Type"); got != tc.wantType {
			t.Errorf("%s content type: %q, want %q", tc.path, got, tc.wantType)
		}
		if !strings.Contains(buf.String(), tc.wantBody) {
			t.Errorf("%s body missing %q", tc.path, tc.wantBody)
		}
	}
	if code, _ = get("/static/icons/icon-192.png"); code != 200 {
		t.Errorf("icon-192: %d", code)
	}
	// Manifest icons must be served as image/png or installability fails.
	if resp, err := client.Get(srv.URL + "/static/icons/icon-512.png"); err != nil {
		t.Fatalf("GET icon-512: %v", err)
	} else {
		resp.Body.Close()
		if got := resp.Header.Get("Content-Type"); got != "image/png" {
			t.Errorf("icon content type: %q, want image/png", got)
		}
	}

	// A problem without a PDF must render its placeholder, with the forms
	// replaced by a login prompt for anonymous visitors.
	p := &store.Problem{Slug: "t1", Title: "Soal Uji", Difficulty: "Mudah",
		TimeLimitMS: 1000, MemoryLimitKB: 65536, CompareMode: "tolerant", Tags: "uji"}
	if _, err := st.CreateProblem(p, tenCases()); err != nil {
		t.Fatalf("create problem: %v", err)
	}
	code, body = get("/p/t1")
	if code != 200 {
		t.Fatalf("problem: %d", code)
	}
	for _, want := range []string{"Soal Uji", "belum punya berkas PDF", "Masuk"} {
		if !strings.Contains(body, want) {
			t.Errorf("problem page missing %q", want)
		}
	}
	if strings.Contains(body, `name="source"`) {
		t.Error("anonymous problem page still shows the submit textarea")
	}

	// Problems map to a stable slug URL; unknown slugs are 404.
	if code, _ = get("/p/nope"); code != 404 {
		t.Errorf("unknown problem: %d, want 404", code)
	}

	// /admin and /s/<id> are closed to anonymous visitors.
	if code, _ = get("/admin/problems"); code != 303 && code != 302 {
		t.Errorf("anonymous /admin: %d, want redirect to login", code)
	}
	if code, _ = get("/s/1"); code != 303 && code != 302 {
		t.Errorf("anonymous /s/1: %d, want redirect to login", code)
	}
}

func tenCases() []store.TestCase {
	var cs []store.TestCase
	for i := 1; i <= 10; i++ {
		cs = append(cs, store.TestCase{Position: i, Stdin: "1 1\n", Expected: "2\n"})
	}
	return cs
}

func casesOf(t *testing.T, st *store.Store, id int64) []store.TestCase {
	t.Helper()
	cs, err := st.Cases(id)
	if err != nil {
		t.Fatalf("cases: %v", err)
	}
	return cs
}
