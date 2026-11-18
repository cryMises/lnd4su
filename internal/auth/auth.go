package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// UsersFile is the DokuWiki credentials file, mounted read-only.
type UsersFile struct {
	Path string
}

type Entry struct {
	Login  string
	Hash   string
	Name   string
	Email  string
	Groups []string
}

// Lookup parses users.auth.php fresh on every call: no cache, so new wiki
// accounts and password changes apply immediately. The file is tiny.
func (u UsersFile) Lookup(login string) (*Entry, error) {
	data, err := os.ReadFile(u.Path)
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) < 2 {
			continue
		}
		if fields[0] != login {
			continue
		}
		e := &Entry{Login: fields[0], Hash: fields[1]}
		if len(fields) > 2 {
			e.Name = fields[2]
		}
		if len(fields) > 3 {
			e.Email = fields[3]
		}
		if len(fields) > 4 {
			for _, g := range strings.Split(fields[4], ",") {
				if g = strings.TrimSpace(g); g != "" {
					e.Groups = append(e.Groups, g)
				}
			}
		}
		return e, nil
	}
	return nil, ErrNoUser
}

var ErrNoUser = errors.New("user not found")

// Verify checks a plaintext password against the stored hash. Never logs,
// stores or returns the hash.
func Verify(e *Entry, password string) bool {
	h := e.Hash
	// Go's bcrypt rejects the $2y$ prefix that PHP (and DokuWiki) writes.
	// $2a$ and $2y$ are the same algorithm; swap the prefix in memory only.
	if strings.HasPrefix(h, "$2y$") {
		h = "$2a$" + h[4:]
	}
	return bcrypt.CompareHashAndPassword([]byte(h), []byte(password)) == nil
}

func IsAdmin(e *Entry) bool {
	for _, g := range e.Groups {
		if g == "admin" {
			return true
		}
	}
	return false
}

// NewToken returns a 256-bit random hex session token.
func NewToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// Lockout is the 5-failures-per-account+IP guard. In memory: a restart clears
// it, which is acceptable for a class-sized judge.
type Lockout struct {
	mu    sync.Mutex
	fails map[string]*failEntry
}

type failEntry struct {
	count int
	until time.Time
}

func NewLockout() *Lockout {
	return &Lockout{fails: map[string]*failEntry{}}
}

const (
	maxFails     = 5
	lockDuration = 5 * time.Minute
)

// Blocked reports whether key (login+"|"+ip) is locked out.
func (l *Lockout) Blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, ok := l.fails[key]
	if !ok {
		return false
	}
	if time.Now().After(f.until) {
		delete(l.fails, key)
		return false
	}
	return f.count >= maxFails
}

// Fail records one failed attempt.
func (l *Lockout) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, ok := l.fails[key]
	if !ok || time.Now().After(f.until) {
		f = &failEntry{}
		l.fails[key] = f
	}
	f.count++
	f.until = time.Now().Add(lockDuration)
}

// Reset clears the counter after a good login.
func (l *Lockout) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
}
