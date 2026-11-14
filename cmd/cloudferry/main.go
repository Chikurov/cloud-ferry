// Command cloudferry is a self-hosted migration assistant: it moves files from
// cloud storage into a folder on the server using rclone as the transfer
// engine, while keeping the engine's shell-equivalent API away from the browser.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cloudferry/internal/health"
	"cloudferry/internal/httpapi"
	"cloudferry/internal/jobs"
	"cloudferry/internal/rc"
	"cloudferry/internal/store"
)

const version = "0.1.0"

func main() {
	listen := flag.String("listen", envOr("LISTEN_ADDR", "0.0.0.0:3000"), "HTTP listen address")
	dataDir := flag.String("data", envOr("DATA_DIR", "/data"), "directory for state.json")
	socket := flag.String("rc-socket", envOr("RC_SOCKET", "/run/rclone/rclone.sock"), "rclone rc unix socket")
	rcUser := flag.String("rc-user", os.Getenv("RC_USER"), "rclone rc basic auth user")
	rcPass := flag.String("rc-pass", os.Getenv("RC_PASS"), "rclone rc basic auth password")
	downloads := flag.String("downloads", envOr("DOWNLOADS_DIR", "/downloads"), "default destination folder")
	destRoots := flag.String("dest-roots", envOr("DEST_ROOTS", ""), "comma separated allowed destination roots (default: downloads)")
	spawn := flag.Bool("spawn-rclone", os.Getenv("SPAWN_RCLONE") == "true", "start rclone rcd as a child process")
	configPath := flag.String("rclone-config", envOr("RCLONE_CONFIG", "/config/rclone.conf"), "rclone config path (used with --spawn-rclone)")
	basicUser := flag.String("basic-user", os.Getenv("CF_BASIC_USER"), "optional HTTP basic auth user")
	basicPass := flag.String("basic-pass", os.Getenv("CF_BASIC_PASS"), "optional HTTP basic auth password")
	transfers := flag.Int("transfers", envInt("CLOUDFERRY_TRANSFERS", 8), "rclone --transfers default")
	checkers := flag.Int("checkers", envInt("CLOUDFERRY_CHECKERS", 16), "rclone --checkers default")
	tpsLimit := flag.Int("tpslimit", envInt("CLOUDFERRY_TPSLIMIT", 20), "rclone --tpslimit default (Yandex API allows 40 rps)")
	healthcheck := flag.Bool("healthcheck", false, "probe the local HTTP server and exit 0 when healthy (used by Docker HEALTHCHECK)")
	flag.Parse()

	if *healthcheck {
		os.Exit(runHealthcheck(healthcheckTarget(*listen), *basicUser, *basicPass))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	roots := []string{*downloads}
	if trimmed := strings.TrimSpace(*destRoots); trimmed != "" {
		roots = nil
		for _, part := range strings.Split(trimmed, ",") {
			p := strings.TrimSpace(part)
			if p == "" {
				continue
			}
			// An optional folderAccess slot that the user has not filled in yet
			// leaves no mount behind; writing there would land in the container
			// layer and be lost on restart.
			if p != *downloads {
				if info, err := os.Stat(p); err != nil || !info.IsDir() {
					continue
				}
			}
			roots = append(roots, p)
		}
	}
	if len(roots) == 0 {
		roots = []string{*downloads}
	}

	if *spawn {
		cleanup, err := spawnRclone(*socket, *configPath, *rcUser, *rcPass)
		if err != nil {
			log.Fatalf("spawn rclone: %v", err)
		}
		defer cleanup()
	}

	st, err := store.Open(*dataDir)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}

	client := rc.NewClient(*socket, *rcUser, *rcPass)
	manager := jobs.NewManager(st, client, jobs.Options{
		Transfers: *transfers,
		Checkers:  *checkers,
		TpsLimit:  *tpsLimit,
		PollEvery: 2 * time.Second,
	})
	if err := manager.Recover(); err != nil {
		log.Printf("recover jobs: %v", err)
	}
	go manager.Run(ctx)

	checker := health.New(st, client, 30*time.Minute)
	go checker.Run(ctx)

	api, err := httpapi.New(httpapi.Config{
		Store:     st,
		Rclone:    client,
		Jobs:      manager,
		Downloads: *downloads,
		DestRoots: roots,
		Version:   version,
		BasicUser: *basicUser,
		BasicPass: *basicPass,
	})
	if err != nil {
		log.Fatalf("init http api: %v", err)
	}

	server := &http.Server{
		Addr:              *listen,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("cloudferry %s listening on %s (rc socket %s)", version, *listen, *socket)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	log.Print("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

// spawnRclone runs `rclone rcd` as a child process. It is meant for local
// development and for single-container installs; in Umbrel the engine runs as
// its own compose service.
func spawnRclone(socket, configPath, user, pass string) (func(), error) {
	args := []string{
		"rcd",
		"--rc-addr", "unix://" + socket,
		"--config", configPath,
		"--rc-job-expire-duration", "24h",
		"--max-stats-groups", "100",
	}
	if user != "" {
		args = append(args, "--rc-user", user, "--rc-pass", pass)
	}
	cmd := exec.Command("rclone", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			if err := cmd.Process.Kill(); err != nil {
				log.Printf("spawn rclone: kill: %v", err)
			}
			return nil, errors.New("rclone did not create the rc socket in time")
		}
		time.Sleep(200 * time.Millisecond)
	}

	return func() {
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			log.Printf("spawn rclone: signal: %v", err)
		}
		done := make(chan struct{})
		go func() {
			if err := cmd.Wait(); err != nil {
				log.Printf("spawn rclone: exit: %v", err)
			}
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			if err := cmd.Process.Kill(); err != nil {
				log.Printf("spawn rclone: kill: %v", err)
			}
		}
	}, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}
