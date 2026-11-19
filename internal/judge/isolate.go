package judge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const progName = "prog"

// toolPath is the PATH handed to the sandboxed compiler. isolate starts
// programs with an empty environment, so gcc would otherwise find neither cc1
// nor as/ld (the latter live in /usr/bin on Debian).
const toolPath = "/usr/local/bin:/usr/bin:/bin"

// Box wraps one isolate box. isolate refuses concurrent use of the same box, so
// every access is serialized by Mu; worker N owns box N.
type Box struct {
	ID      int
	Isolate string
	Root    string // box root on the host, e.g. /var/local/isolate
	Mu      *sync.Mutex
}

func NewBox(id int, isolatePath, root string) *Box {
	return &Box{ID: id, Isolate: isolatePath, Root: root, Mu: &sync.Mutex{}}
}

// isolate uses <box_root>/<id> as the box dir and <box_root>/<id>/box as the
// chroot the program sees as /.
func (b *Box) dir() string      { return filepath.Join(b.Root, strconv.Itoa(b.ID)) }
func (b *Box) boxd() string     { return filepath.Join(b.dir(), "box") }
func (b *Box) progPath() string { return filepath.Join(b.boxd(), progName) }

// Init (re)creates the sandbox.
func (b *Box) Init(ctx context.Context) error {
	b.Cleanup(ctx)
	out, err := b.isolateCmd(ctx, "--init").CombinedOutput()
	if err != nil {
		return fmt.Errorf("isolate --init: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if err := os.MkdirAll(b.boxd(), 0o777); err != nil {
		return err
	}
	// isolate creates <id>/box as 0700 owned by the invoking user (root), but
	// the program inside runs as an unprivileged box uid, so it could neither
	// write the compiled binary nor touch /box at all.
	return os.Chmod(b.boxd(), 0o777)
}

func (b *Box) Cleanup(ctx context.Context) {
	_ = b.isolateCmd(ctx, "--cleanup").Run()
	_ = os.RemoveAll(b.dir())
}

func (b *Box) isolateCmd(ctx context.Context, args ...string) *exec.Cmd {
	full := append([]string{"--cg", "--box-id", strconv.Itoa(b.ID), "--silent"}, args...)
	return exec.CommandContext(ctx, b.Isolate, full...)
}

// Write places a file into the box (visible as /box/<name> in the sandbox).
func (b *Box) Write(name string, data []byte, mode os.FileMode) error {
	return os.WriteFile(filepath.Join(b.boxd(), name), data, mode)
}

// Read reads at most capBytes from a box file; truncated reports extra data.
func (b *Box) Read(name string, capBytes int64) (string, bool, error) {
	return readCapped(filepath.Join(b.boxd(), name), capBytes)
}

// ReadAt reads a file that isolate itself writes next to the box dir (the meta
// file: --meta is opened on the host, before the chroot exists).
func (b *Box) ReadAt(name string, capBytes int64) (string, bool, error) {
	return readCapped(filepath.Join(b.dir(), name), capBytes)
}

func readCapped(path string, capBytes int64) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, capBytes+1))
	if err != nil {
		return "", false, err
	}
	if int64(len(buf)) > capBytes {
		return string(buf[:capBytes]), true, nil
	}
	return string(buf), false, nil
}

// Meta is the parsed isolate meta file.
type Meta struct {
	Status   string // "" (ok), RE, SG, TO, XX
	Message  string
	TimeS    float64
	WallS    float64
	MaxRSSKB int64
	CgMemKB  int64
	OOM      bool
	Killed   bool
	ExitCode *int
	ExitSig  *int
}

func parseMeta(data string) Meta {
	var m Meta
	for _, line := range strings.Split(data, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		switch k {
		case "status":
			m.Status = v
		case "message":
			m.Message = v
		case "time":
			m.TimeS, _ = strconv.ParseFloat(v, 64)
		case "time-wall":
			m.WallS, _ = strconv.ParseFloat(v, 64)
		case "max-rss":
			m.MaxRSSKB, _ = strconv.ParseInt(v, 10, 64)
		case "cg-mem":
			m.CgMemKB, _ = strconv.ParseInt(v, 10, 64)
		case "cg-oom-killed":
			m.OOM = true
		case "killed":
			m.Killed = true
		case "exitcode":
			n, _ := strconv.Atoi(v)
			m.ExitCode = &n
		case "exitsig":
			n, _ := strconv.Atoi(v)
			m.ExitSig = &n
		}
	}
	return m
}

// RunSpec describes one sandboxed execution.
type RunSpec struct {
	Args      []string // program + args inside the sandbox
	TimeMS    int
	WallMS    int
	MemKB     int64
	ExtraTime float64
	Compile   bool // compile: allow child processes
}

// Run executes spec inside the box and returns meta facts.
//
// The meta file is opened by isolate on the host before the sandbox exists, so
// it needs an absolute host path. --stdin/--stdout/--stderr are opened *inside*
// the chroot (setup_fds runs after setup_root), so they must stay relative to
// /box, where the box dir is mounted read-write.
func (b *Box) Run(ctx context.Context, spec RunSpec) (Meta, error) {
	args := []string{
		"--run",
		"--time=" + strconv.FormatFloat(float64(spec.TimeMS)/1000, 'f', 3, 64),
		"--wall-time=" + strconv.FormatFloat(float64(spec.WallMS)/1000, 'f', 3, 64),
		// --mem caps address space and --cg-mem caps resident pages. Setting
		// them equal makes malloc fail just before the OOM kill, which shows up
		// as RE instead of MLE; the address-space cap is only a backstop.
		"--mem=" + strconv.FormatInt(spec.MemKB*2, 10),
		"--cg-mem=" + strconv.FormatInt(spec.MemKB, 10),
		"--fsize=2048", // KB per file; caps runaway output (compile overrides)
		"--meta=" + filepath.Join(b.dir(), "meta.txt"),
		"--stdin=input.txt",
		"--stdout=output.txt",
		"--stderr=stderr.txt",
	}
	if spec.Compile {
		// isolate starts the program with a nearly empty environment (it only
		// sets LIBC_FATAL_STDERR_). gcc locates cc1/as/ld through PATH, so
		// without this every compile dies with "error trying to exec 'as'".
		// The compiled binary also needs more room than a run's output cap:
		// heavy C++ STL programs land well above 2 MB.
		args = append(args, "--processes=64", "--env=PATH="+toolPath)
		args = append(args, "--fsize=16384") // last --fsize wins
	}
	if spec.ExtraTime > 0 {
		args = append(args, "--extra-time="+strconv.FormatFloat(spec.ExtraTime, 'f', 3, 64))
	}
	args = append(args, "--")
	args = append(args, spec.Args...)

	_ = b.isolateCmd(ctx, args...).Run() // non-zero exit is normal; meta holds truth

	metaData, _, err := b.ReadAt("meta.txt", 64*1024)
	if err != nil {
		return Meta{Status: "XX", Message: "cannot read meta: " + err.Error()}, nil
	}
	m := parseMeta(metaData)
	if m.Status == "XX" {
		return m, fmt.Errorf("isolate internal error: %s", m.Message)
	}
	return m, nil
}

// classify turns meta into a verdict given the limits used.
func classify(m Meta, timeMS int, memKB int64) string {
	if m.Status == "TO" {
		return "TLE"
	}
	memUsed := m.MaxRSSKB
	if m.CgMemKB > memUsed {
		memUsed = m.CgMemKB
	}
	if m.OOM || (m.Status == "SG" && memUsed >= memKB*90/100) {
		return "MLE"
	}
	if m.Status == "RE" || m.Status == "SG" {
		return "RE"
	}
	if m.TimeS*1000 > float64(timeMS) {
		return "TLE"
	}
	return "OK"
}

// Compile compiles source inside the sandbox. Serialization across workers is
// the caller's job. Returns (ok, compilerOutput).
func (b *Box) Compile(ctx context.Context, lang string, source []byte, filename string) (bool, string) {
	if err := b.Write(filename, source, 0o644); err != nil {
		return false, "internal: cannot stage source: " + err.Error()
	}
	compiler, std := "/usr/bin/gcc", "-std=c17"
	if lang == "cpp" {
		compiler, std = "/usr/bin/g++", "-std=c++17"
	}
	m, err := b.Run(ctx, RunSpec{
		Compile: true,
		Args:    []string{compiler, "-O2", std, "-o", progName, "/box/" + filename},
		TimeMS:  10000,
		WallMS:  20000,
		MemKB:   512 * 1024,
	})
	if err != nil {
		return false, "internal: " + err.Error()
	}
	errOut, _, _ := b.Read("stderr.txt", 256*1024)
	if m.Status != "" || m.ExitCode == nil || *m.ExitCode != 0 {
		if errOut == "" {
			errOut = "compiler gagal tanpa pesan"
			if m.Status != "" {
				errOut += " (status=" + m.Status + ")"
			}
		}
		if m.Status == "TO" {
			errOut += "\n(compiler melebihi batas waktu)"
		}
		return false, errOut
	}
	if _, statErr := os.Stat(b.progPath()); statErr != nil {
		return false, "compiler tidak menghasilkan executable"
	}
	return true, errOut
}

// RunCase runs the staged program with stdin; returns (stdout, truncated, meta, verdict).
func (b *Box) RunCase(ctx context.Context, stdin string, timeMS int, memKB, outCap int64) (string, bool, Meta, string) {
	if err := b.Write("input.txt", []byte(stdin), 0o644); err != nil {
		return "", false, Meta{Status: "XX"}, "XX"
	}
	m, err := b.Run(ctx, RunSpec{
		Args:      []string{"/box/" + progName},
		TimeMS:    timeMS,
		WallMS:    timeMS*3 + 1000, // backstop for sleeping programs
		MemKB:     memKB,
		ExtraTime: 0.3,
	})
	if err != nil {
		return "", false, m, "XX"
	}
	out, truncated, _ := b.Read("output.txt", outCap)
	return out, truncated, m, classify(m, timeMS, memKB)
}

// Reset wipes and re-creates the box.
func (b *Box) Reset(ctx context.Context) {
	b.Cleanup(ctx)
	_ = b.Init(ctx)
}
