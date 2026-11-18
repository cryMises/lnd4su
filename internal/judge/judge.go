package judge

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/crymises/lnd4su/internal/store"
)

// Config holds the runtime knobs from the environment.
type Config struct {
	Workers     int    // 1 or 2 (Phase 0b verdict)
	IsolatePath string // /usr/local/bin/isolate
	BoxRoot     string // /var/local/isolate
}

// Judge owns the worker pool, the compile mutex and the isolate boxes.
type Judge struct {
	cfg   Config
	st    *store.Store
	boxes []*Box

	compileMu chan struct{} // capacity 1 = the compile mutex
	wake      chan struct{}
}

const (
	OutCapBytes   = 1 << 20  // 1 MB per case output
	SrcCapBytes   = 64 << 10 // 64 KB source
	compileMemKB  = 512 << 10
	compileTimeMS = 10000
)

func New(cfg Config, st *store.Store) *Judge {
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	if cfg.Workers > 2 {
		cfg.Workers = 2
	}
	if cfg.IsolatePath == "" {
		cfg.IsolatePath = "/usr/local/bin/isolate"
	}
	if cfg.BoxRoot == "" {
		cfg.BoxRoot = "/var/local/isolate"
	}
	j := &Judge{
		cfg:       cfg,
		st:        st,
		compileMu: make(chan struct{}, 1),
		wake:      make(chan struct{}, 16),
	}
	for i := 0; i < cfg.Workers; i++ {
		j.boxes = append(j.boxes, NewBox(i, cfg.IsolatePath, cfg.BoxRoot))
	}
	return j
}

// Start launches the worker goroutines. Any submission left queued by a
// restart is picked up by the normal polling loop.
func (j *Judge) Start(ctx context.Context) error {
	if err := EnsureHost(j.cfg.BoxRoot); err != nil {
		return err
	}
	// A submission interrupted by a restart stays "running" forever otherwise.
	if n, err := j.st.RequeueInterrupted(); err != nil {
		return fmt.Errorf("requeue interrupted: %w", err)
	} else if n > 0 {
		log.Printf("judge: requeued %d interrupted submission(s)", n)
	}
	for _, box := range j.boxes {
		if err := box.Init(ctx); err != nil {
			return fmt.Errorf("box %d init: %w", box.ID, err)
		}
		go j.worker(ctx, box)
	}
	log.Printf("judge: %d worker(s), box root %s", len(j.boxes), j.cfg.BoxRoot)
	return nil
}

// Submit nudges the workers after a new submission row exists.
func (j *Judge) Submit() {
	select {
	case j.wake <- struct{}{}:
	default:
	}
}

func (j *Judge) worker(ctx context.Context, box *Box) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		sub, err := j.st.NextQueued()
		if err != nil {
			log.Printf("judge w%d: dequeue: %v", box.ID, err)
		}
		if sub != nil {
			j.runSubmission(ctx, box, sub)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-j.wake:
		case <-ticker.C:
		}
	}
}

func (j *Judge) runSubmission(ctx context.Context, box *Box, sub *store.Submission) {
	// Run mode shares box 0, so a worker must hold the same lock it does.
	box.Mu.Lock()
	defer box.Mu.Unlock()

	start := time.Now()
	prob, err := j.st.ProblemByID(sub.ProblemID)
	if err != nil {
		log.Printf("judge: submission %d: problem %d: %v", sub.ID, sub.ProblemID, err)
		_ = j.st.FinishSubmission(sub.ID, "RE", 0, nil)
		return
	}
	cases, err := j.st.Cases(prob.ID)
	if err != nil {
		log.Printf("judge: submission %d: cases: %v", sub.ID, err)
		_ = j.st.FinishSubmission(sub.ID, "RE", 0, nil)
		return
	}

	if err := box.Init(ctx); err != nil {
		log.Printf("judge: submission %d: init: %v", sub.ID, err)
		_ = j.st.FinishSubmission(sub.ID, "RE", 0, nil)
		return
	}
	defer box.Cleanup(ctx)

	// 1. compile under the global mutex (never two gcc at once)
	file := "main.c"
	if sub.Language == "cpp" {
		file = "main.cpp"
	}
	j.compileMu <- struct{}{}
	ok, compOut := box.Compile(ctx, sub.Language, []byte(sub.Source), file)
	<-j.compileMu

	if !ok {
		results := make([]store.CaseResult, 0, len(cases))
		for _, c := range cases {
			results = append(results, store.CaseResult{Position: c.Position, Verdict: "CE"})
		}
		_ = j.st.FinishSubmission(sub.ID, "CE", 0, results)
		_ = j.st.SetCompileLog(sub.ID, compOut)
		log.Printf("judge: submission %d CE (%s)", sub.ID, time.Since(start).Round(time.Millisecond))
		return
	}

	prog, err := os.ReadFile(box.progPath())
	if err != nil {
		log.Printf("judge: submission %d: read prog: %v", sub.ID, err)
		_ = j.st.FinishSubmission(sub.ID, "RE", 0, nil)
		return
	}

	results := make([]store.CaseResult, 0, len(cases))
	passed := 0
	for _, c := range cases {
		res := evaluateCase(ctx, box, prog, prob, c)
		if res.Verdict == "AC" {
			passed++
		}
		results = append(results, res)
	}

	verdict := overallVerdict(results)
	if err := j.st.FinishSubmission(sub.ID, verdict, passed, results); err != nil {
		log.Printf("judge: submission %d: finish: %v", sub.ID, err)
	}
	log.Printf("judge: submission %d %s %d/10 (%s)", sub.ID, verdict, passed, time.Since(start).Round(time.Millisecond))
}

// evaluateCase runs one case in a fresh box (fresh cgroup accounting).
func evaluateCase(ctx context.Context, box *Box, prog []byte, prob *store.Problem, c store.TestCase) store.CaseResult {
	res := store.CaseResult{Position: c.Position}
	if err := box.Init(ctx); err != nil {
		res.Verdict = "XX"
		log.Printf("judge: case %d init: %v", c.Position, err)
		return res
	}
	defer box.Cleanup(ctx)
	if err := box.Write(progName, prog, 0o755); err != nil {
		res.Verdict = "XX"
		return res
	}
	out, truncated, meta, verdict := box.RunCase(ctx, c.Stdin, prob.TimeLimitMS, int64(prob.MemoryLimitKB), OutCapBytes)
	res.TimeMS = int64(meta.TimeS * 1000)
	res.MemoryKB = meta.MaxRSSKB
	if meta.CgMemKB > res.MemoryKB {
		res.MemoryKB = meta.CgMemKB
	}
	res.Truncated = truncated
	switch {
	case verdict == "OK" && compare(out, c.Expected, prob.CompareMode):
		res.Verdict = "AC"
	case verdict == "OK":
		res.Verdict = "WA"
	default:
		res.Verdict = verdict
	}
	if res.Verdict != "AC" {
		res.Stdin, res.Expected, res.Actual = c.Stdin, c.Expected, out
	}
	return res
}

func overallVerdict(results []store.CaseResult) string {
	for _, r := range results {
		if r.Verdict != "AC" {
			switch r.Verdict {
			case "TLE", "MLE", "RE", "CE":
				return r.Verdict
			default:
				return "WA"
			}
		}
	}
	return "AC"
}

// compare implements both modes from the spec.
func compare(got, want, mode string) bool {
	if mode == "exact" {
		return got == want
	}
	return normalize(got) == normalize(want)
}

// normalize: CRLF -> LF, trailing whitespace per line dropped, trailing blank
// lines dropped.
func normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// RunResult is the Run-mode path: compile + one run with custom stdin, nothing
// recorded.
type RunResult struct {
	Verdict   string
	Compile   string
	CompileOK bool
	Stdout    string
	Stderr    string
	TimeMS    int64
	MemKB     int64
	Truncated bool
}

func (j *Judge) RunCustom(ctx context.Context, lang, source, stdin string, timeMS int, memKB int64) (*RunResult, error) {
	if len(source) > SrcCapBytes {
		return nil, fmt.Errorf("kode melebihi 64 KB")
	}
	box := j.boxes[0]
	box.Mu.Lock()
	defer box.Mu.Unlock()

	if err := box.Init(ctx); err != nil {
		return nil, err
	}
	defer box.Cleanup(ctx)

	file := "main.c"
	if lang == "cpp" {
		file = "main.cpp"
	}
	j.compileMu <- struct{}{}
	ok, compOut := box.Compile(ctx, lang, []byte(source), file)
	<-j.compileMu
	rr := &RunResult{CompileOK: ok, Compile: compOut}
	if !ok {
		rr.Verdict = "CE"
		return rr, nil
	}
	prog, err := os.ReadFile(box.progPath())
	if err != nil {
		return nil, err
	}
	box.Reset(ctx)
	if err := box.Write(progName, prog, 0o755); err != nil {
		return nil, err
	}
	out, truncated, meta, verdict := box.RunCase(ctx, stdin, timeMS, memKB, OutCapBytes)
	stderr, _, _ := box.Read("stderr.txt", OutCapBytes)
	rr.Verdict = verdict
	rr.Stdout, rr.Truncated = out, truncated
	rr.Stderr = stderr
	rr.TimeMS = int64(meta.TimeS * 1000)
	rr.MemKB = meta.MaxRSSKB
	if meta.CgMemKB > rr.MemKB {
		rr.MemKB = meta.CgMemKB
	}
	return rr, nil
}
