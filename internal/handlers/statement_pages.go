package handlers

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// maxStatementPages caps how many pages of a statement are rendered as
// images. Statements are short by design (one problem = a few pages).
const maxStatementPages = 50

// pagesMu serializes page-image generation so two first views of the same
// problem do not run pdftoppm twice.
var pagesMu sync.Mutex

// statementPages returns the rendered page-image filenames for a problem,
// generating them with pdftoppm on first use. The PDF stays the source of
// truth; the images are a derived cache under statements/<id>/pages/.
// An empty list with a nil error means "no PDF yet". An error means
// generation failed and the caller should fall back to the plain PDF link.
func (a *App) statementPages(problemID int64, pdfName string) ([]string, error) {
	pagesMu.Lock()
	defer pagesMu.Unlock()
	pagesDir := filepath.Join(a.DataDir, "statements", fmt.Sprint(problemID), "pages")
	if names, err := listPageImages(pagesDir); err == nil && len(names) > 0 {
		return names, nil
	}
	pdfPath := filepath.Join(a.DataDir, "statements", pdfName)
	if _, err := os.Stat(pdfPath); err != nil {
		return nil, nil // no PDF on disk (yet)
	}
	tmpDir, err := os.MkdirTemp("", "lnd4su-pages-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pdftoppm", "-png", "-r", "150",
		"-f", "1", "-l", fmt.Sprint(maxStatementPages),
		pdfPath, filepath.Join(tmpDir, "page"))
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("pdftoppm: %v: %s", err, strings.TrimSpace(string(out)))
		return nil, fmt.Errorf("gagal mengubah PDF menjadi gambar: %w", err)
	}
	entries, err := filepath.Glob(filepath.Join(tmpDir, "page-*.png"))
	if err != nil || len(entries) == 0 {
		return nil, fmt.Errorf("pdftoppm tidak menghasilkan halaman")
	}
	sort.Strings(entries)
	if err := os.MkdirAll(pagesDir, 0o755); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for i, src := range entries {
		name := fmt.Sprintf("page-%02d.png", i+1)
		dst := filepath.Join(pagesDir, name)
		if err := os.Rename(src, dst); err != nil {
			b, rerr := os.ReadFile(src)
			if rerr != nil {
				return nil, rerr
			}
			if werr := os.WriteFile(dst, b, 0o644); werr != nil {
				return nil, werr
			}
		}
		names = append(names, name)
	}
	return names, nil
}

func listPageImages(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasPrefix(n, "page-") && strings.HasSuffix(n, ".png") {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names, nil
}

// dropStatementPages removes a problem's derived page images. Called when
// its PDF is replaced or the problem is deleted; the next view regenerates.
func (a *App) dropStatementPages(problemID int64) {
	pagesMu.Lock()
	defer pagesMu.Unlock()
	_ = os.RemoveAll(filepath.Join(a.DataDir, "statements", fmt.Sprint(problemID), "pages"))
}

// withPages fills d.Pages from the problem's rendered statement pages.
// Failures are logged and leave Pages empty (the template falls back to
// the plain PDF link).
func (a *App) withPages(p *store.Problem, d *problemData) {
	if !p.HasPDF {
		return
	}
	name, ok := a.St.StatementFile(p.ID)
	if !ok {
		return
	}
	pages, err := a.statementPages(p.ID, name)
	if err != nil {
		log.Printf("statement pages %s: %v", p.Slug, err)
		return
	}
	d.Pages = pages
}
