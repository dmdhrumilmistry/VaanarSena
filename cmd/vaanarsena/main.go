// Command vaanarsena is the VaanarSena MDM server.
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dmdhrumilmistry/VaanarSena/internal/agent"
	"github.com/dmdhrumilmistry/VaanarSena/internal/android"
	"github.com/dmdhrumilmistry/VaanarSena/internal/api"
	"github.com/dmdhrumilmistry/VaanarSena/internal/apple"
	"github.com/dmdhrumilmistry/VaanarSena/internal/auth"
	"github.com/dmdhrumilmistry/VaanarSena/internal/chromeos"
	"github.com/dmdhrumilmistry/VaanarSena/internal/config"
	"github.com/dmdhrumilmistry/VaanarSena/internal/httpx"
	"github.com/dmdhrumilmistry/VaanarSena/internal/mdm"
	"github.com/dmdhrumilmistry/VaanarSena/internal/pki"
	"github.com/dmdhrumilmistry/VaanarSena/internal/secrets"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
	"github.com/dmdhrumilmistry/VaanarSena/internal/web"
	"github.com/dmdhrumilmistry/VaanarSena/internal/windows"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func usage() {
	fmt.Fprintf(os.Stderr, `VaanarSena %s - open source device management

Usage:
  vaanarsena serve                 run the server (applies migrations first)
  vaanarsena migrate               apply database migrations and exit
  vaanarsena create-admin -email E create an admin; reads the password from
                                   VS_ADMIN_PASSWORD or the first line of stdin
  vaanarsena version               print the version

Configuration is read from VS_* environment variables; see docs/configuration.md.
`, version)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve()
	case "migrate":
		err = migrate()
	case "create-admin":
		err = createAdmin(os.Args[2:])
	case "version":
		fmt.Println(version)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func logger(level string) *slog.Logger {
	var l slog.Level
	_ = l.UnmarshalText([]byte(level))
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}

func open(ctx context.Context) (*config.Config, *store.Store, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	// The database often starts alongside the server (compose, Kubernetes), so
	// wait for it rather than crash-looping.
	var st *store.Store
	deadline := time.Now().Add(2 * time.Minute)
	for {
		st, err = store.Open(ctx, cfg.DatabaseURL)
		if err == nil {
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, nil, err
		}
		fmt.Fprintln(os.Stderr, "waiting for database:", err)
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	if err := st.Migrate(ctx); err != nil {
		st.Close()
		return nil, nil, fmt.Errorf("migrate: %w", err)
	}
	return cfg, st, nil
}

func migrate() error {
	_, st, err := open(context.Background())
	if err != nil {
		return err
	}
	st.Close()
	fmt.Println("migrations applied")
	return nil
}

func createAdmin(args []string) error {
	fs := flag.NewFlagSet("create-admin", flag.ExitOnError)
	email := fs.String("email", "", "admin email")
	name := fs.String("name", "", "display name")
	_ = fs.Parse(args)
	if *email == "" {
		return errors.New("-email is required")
	}
	pw := os.Getenv("VS_ADMIN_PASSWORD")
	if pw == "" {
		fmt.Fprint(os.Stderr, "password: ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return err
		}
		pw = strings.TrimRight(line, "\r\n")
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	ctx := context.Background()
	_, st, err := open(ctx)
	if err != nil {
		return err
	}
	defer st.Close()
	u, err := st.CreateUser(ctx, *email, *name, hash, store.RoleAdmin)
	if err != nil {
		return err
	}
	_ = st.Audit(ctx, "cli", "user.create", u.ID, map[string]any{"email": u.Email, "role": u.Role}, "")
	fmt.Println("created admin", u.Email)
	return nil
}

// bootstrapAdmin creates the first admin from the environment when the user
// table is empty, so container deployments need no interactive step.
func bootstrapAdmin(ctx context.Context, st *store.Store, log *slog.Logger) error {
	email, pw := os.Getenv("VS_BOOTSTRAP_ADMIN_EMAIL"), os.Getenv("VS_BOOTSTRAP_ADMIN_PASSWORD")
	if email == "" || pw == "" {
		return nil
	}
	users, err := st.ListUsers(ctx)
	if err != nil || len(users) > 0 {
		return err
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return fmt.Errorf("VS_BOOTSTRAP_ADMIN_PASSWORD: %w", err)
	}
	u, err := st.CreateUser(ctx, email, "Administrator", hash, store.RoleAdmin)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") {
			return nil // another replica won the race
		}
		return err
	}
	_ = st.Audit(ctx, "system", "user.bootstrap", u.ID, map[string]any{"email": u.Email}, "")
	log.Info("created bootstrap admin", "email", u.Email)
	return nil
}

func serve() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, st, err := open(ctx)
	if err != nil {
		return err
	}
	defer st.Close()
	log := logger(cfg.LogLevel)
	httpx.TrustProxy(config.EnvBool("VS_TRUST_PROXY", false))

	box, err := secrets.NewBox(cfg.SecretKey)
	if err != nil {
		return err
	}
	var ca *pki.CA
	if cfg.CACertFile != "" {
		ca, err = pki.LoadFiles(cfg.CACertFile, cfg.CAKeyFile)
	} else {
		ca, err = pki.LoadOrCreate(ctx, st, box, cfg.OrgName)
	}
	if err != nil {
		return err
	}
	if err := bootstrapAdmin(ctx, st, log); err != nil {
		return err
	}

	svc := mdm.New(st, log)
	mux := http.NewServeMux()
	secure := strings.HasPrefix(cfg.PublicURL, "https://")
	authn := auth.New(st, box, cfg.SessionTTL, secure)
	apiSrv := &api.API{Store: st, Auth: authn, Svc: svc, CA: ca, PublicURL: cfg.PublicURL, Org: cfg.OrgName, Version: version, Log: log}

	if cfg.AppleEnabled() {
		pusher, err := apple.NewPusher(cfg.APNsCertFile, cfg.APNsKeyFile, cfg.APNsTopic)
		if err != nil {
			return err
		}
		if time.Until(pusher.Expiry) < 30*24*time.Hour {
			log.Warn("APNs push certificate expires soon; renew it at identity.apple.com with the same Apple ID", "expires", pusher.Expiry)
		}
		drv := &apple.Driver{Store: st, Svc: svc, CA: ca, Box: box, Pusher: pusher, Org: cfg.OrgName,
			PublicURL: cfg.PublicURL, CertHeader: cfg.ClientCertHeader, Log: log}
		svc.Register(drv)
		drv.Routes(mux)
		log.Info("apple MDM enabled", "topic", pusher.Topic)
	}

	win := &windows.Driver{Store: st, Svc: svc, CA: ca, Org: cfg.OrgName, PublicURL: cfg.PublicURL, CertHeader: cfg.ClientCertHeader, Log: log}
	svc.Register(win)
	win.Routes(mux)

	lin := &agent.Driver{Store: st, Svc: svc, CA: ca, CertHeader: cfg.ClientCertHeader, Log: log}
	svc.Register(lin)
	lin.Routes(mux)

	if cfg.AndroidEnabled() {
		drv, err := android.New(ctx, st, svc, cfg.GoogleCredentialsFile, cfg.AndroidEnterprise, log)
		if err != nil {
			return fmt.Errorf("android: %w", err)
		}
		svc.Register(drv)
		apiSrv.Android = drv
		go drv.Run(ctx, 5*time.Minute)
		log.Info("android management enabled", "enterprise", cfg.AndroidEnterprise)
	}
	if cfg.ChromeOSEnabled() {
		drv, err := chromeos.New(ctx, st, cfg.GoogleCredentialsFile, cfg.GoogleAdminSubject, cfg.GoogleCustomerID, log)
		if err != nil {
			return fmt.Errorf("chromeos: %w", err)
		}
		svc.Register(drv)
		go drv.Run(ctx, 15*time.Minute)
		log.Info("chromeos management enabled")
	}

	apiSrv.Routes(mux)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := st.DB.Ping(r.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/", web.Handler())

	go housekeeping(ctx, st, log)

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           withMiddleware(mux, log),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	if cfg.TLSCertFile != "" {
		// Ask for (but do not require) a client certificate so Windows and
		// Linux devices can authenticate with their VaanarSena identity.
		srv.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			ClientAuth: tls.VerifyClientCertIfGiven,
			ClientCAs:  ca.Pool(),
		}
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.ListenAddr, "publicUrl", cfg.PublicURL, "tls", cfg.TLSCertFile != "", "version", version)
		if cfg.TLSCertFile != "" {
			errc <- srv.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
		} else {
			errc <- srv.ListenAndServe()
		}
	}()
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func housekeeping(ctx context.Context, st *store.Store, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if err := st.PrunePendingIdentities(ctx); err != nil && ctx.Err() == nil {
			log.Warn("prune pending identities", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

func withMiddleware(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/" || !strings.Contains(r.URL.Path, ".") {
			h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		}
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("panic", "err", rec, "path", r.URL.Path)
				http.Error(sw, "internal error", http.StatusInternalServerError)
			}
			if r.URL.Path != "/healthz" && r.URL.Path != "/readyz" {
				log.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.code,
					"duration_ms", time.Since(start).Milliseconds(), "ip", httpx.ClientIP(r))
			}
		}()
		next.ServeHTTP(sw, r)
	})
}
