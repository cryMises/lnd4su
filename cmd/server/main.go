package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/crymises/lnd4su/internal/auth"
	"github.com/crymises/lnd4su/internal/handlers"
	"github.com/crymises/lnd4su/internal/judge"
	"github.com/crymises/lnd4su/internal/store"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	dataDir := env("LND4SU_DATA", "/data")
	port := env("LND4SU_PORT", "8080")
	workers, _ := strconv.Atoi(env("LND4SU_WORKERS", "1"))
	isolatePath := env("LND4SU_ISOLATE", "/usr/local/bin/isolate")
	boxRoot := env("LND4SU_BOXROOT", "/var/local/isolate")
	usersFile := env("LND4SU_USERS", "/wiki-conf/users.auth.php")
	wikiURL := env("LND4SU_WIKI_URL", "https://crymises-music.centralindia.cloudapp.azure.com:8453")

	for _, d := range []string{dataDir, filepath.Join(dataDir, "statements")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			log.Fatalf("data dir: %v", err)
		}
	}

	st, err := store.Open(filepath.Join(dataDir, "lnd4su.db"))
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	j := judge.New(judge.Config{Workers: workers, IsolatePath: isolatePath, BoxRoot: boxRoot}, st)
	if err := j.Start(ctx); err != nil {
		log.Fatalf("judge: %v", err)
	}

	app := &handlers.App{
		St:      st,
		Users:   auth.UsersFile{Path: usersFile},
		Lockout: auth.NewLockout(),
		Judge:   j,
		DataDir: dataDir,
		WikiURL: wikiURL,
	}

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           app.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shut)
	}()

	log.Printf("LND4SU listening on :%s (workers=%d, data=%s)", port, workers, dataDir)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("http: %v", err)
	}
}
