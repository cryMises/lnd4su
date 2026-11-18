package handlers

import "testing"

// Templates are parsed lazily on the first render; a syntax error would
// otherwise only surface as a 500 at runtime.
func TestTemplatesParse(t *testing.T) {
	a := &App{WikiURL: "https://example.test"}
	if err := a.loadTemplates(); err != nil {
		t.Fatalf("templates: %v", err)
	}
	for _, page := range []string{
		"index.html", "problem.html", "submission.html", "login.html",
		"me.html", "admin.html", "problem_form.html",
	} {
		if a.tmpl.Lookup(page) == nil {
			t.Errorf("template %q not defined", page)
		}
	}
}
