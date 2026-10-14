// Command backsoon-admin serves the backsoon administration panel.
//
// With -healthcheck it does not start a server: it asks the panel already running on this host
// for /healthz and exits 0 only on a 200, which is what a distroless container needs.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jorgealonsodev/backsoon/admin/internal/config"
	"github.com/jorgealonsodev/backsoon/admin/internal/pages"
	"github.com/jorgealonsodev/backsoon/admin/internal/publish"
	"github.com/jorgealonsodev/backsoon/admin/internal/users"
	"github.com/jorgealonsodev/backsoon/admin/internal/web"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("backsoon-admin", flag.ContinueOnError)
	flags.SetOutput(stderr)
	health := flags.Bool("healthcheck", false, "check the running panel's /healthz and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *health {
		return healthcheck(getenv, stderr)
	}

	cfg, err := config.Load(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "backsoon-admin: cannot start, fix the configuration:\n%v\n", err)
		return 1
	}
	logger := log.New(stdout, "", log.LstdFlags)
	if cfg.PlaintextPassword {
		logger.Printf("warning: %s holds the password in plaintext; set %s to a bcrypt hash instead",
			config.EnvPassword, config.EnvPasswordHash)
	}

	if cfg.TrustedProxies == nil {
		logger.Printf("login rate limit: X-Real-IP trusted from any peer; set %s to the proxy network range",
			config.EnvTrustedProxies)
	} else {
		ranges := make([]string, len(cfg.TrustedProxies))
		for i, p := range cfg.TrustedProxies {
			ranges[i] = p.String()
		}
		logger.Printf("login rate limit: X-Real-IP trusted from %s", strings.Join(ranges, ", "))
	}

	store, err := pages.Open(pages.Options{
		MetaDir:      cfg.MetaDir,
		PagesDir:     cfg.PagesDir,
		TemplatePath: cfg.TemplatePath,
		Refresh:      cfg.Refresh,
		Publish:      publish.Publisher(cfg.PagesDir),
	})
	if err != nil {
		fmt.Fprintf(stderr, "backsoon-admin: cannot open the page store: %v\n", err)
		return 1
	}
	userStore, err := users.Open(users.Options{MetaDir: cfg.MetaDir, Owner: cfg.User})
	if err != nil {
		fmt.Fprintf(stderr, "backsoon-admin: cannot open the user store: %v\n", err)
		return 1
	}
	handler, err := web.New(web.Options{
		Store:               store,
		Users:               userStore,
		User:                cfg.User,
		PasswordHash:        cfg.PasswordHash,
		PasswordFingerprint: cfg.PasswordFingerprint,
		SessionSecret:       cfg.SessionSecret,
		Logger:              logger,
		TrustedProxies:      cfg.TrustedProxies,
	})
	if err != nil {
		fmt.Fprintf(stderr, "backsoon-admin: %v\n", err)
		return 1
	}

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		fmt.Fprintf(stderr, "backsoon-admin: cannot listen: %v\n", err)
		return 1
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    32 << 10,
		ErrorLog:          logger,
	}
	logger.Printf("listening on %s", ln.Addr())

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	select {
	case err := <-serveErr:
		fmt.Fprintf(stderr, "backsoon-admin: server stopped: %v\n", err)
		return 1
	case <-ctx.Done():
	}

	logger.Printf("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		fmt.Fprintf(stderr, "backsoon-admin: shutdown: %v\n", err)
		return 1
	}
	return 0
}

// healthURL targets the loopback address on the port of BACKSOON_ADMIN_LISTEN. It reads nothing
// else, so it works without the administrator credentials.
func healthURL(getenv func(string) string) string {
	listen := getenv(config.EnvListen)
	if listen == "" {
		listen = config.DefaultListen
	}
	_, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		port = "8080"
	}
	return "http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz"
}

func healthcheck(getenv func(string) string, stderr io.Writer) int {
	client := &http.Client{
		Timeout:       2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get(healthURL(getenv))
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck failed: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "healthcheck failed: status %d\n", resp.StatusCode)
		return 1
	}
	return 0
}
