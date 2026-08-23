package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"jc_proxy/internal/admin"
	"jc_proxy/internal/config"
	"jc_proxy/internal/gateway"
	"jc_proxy/internal/keystore"
)

func main() {
	configPath := flag.String("config", "./config.yaml", "config file path")
	flag.Parse()

	bootstrapPath := strings.TrimSpace(*configPath)
	var (
		bootstrapCfg *config.Config
		err          error
	)
	switch {
	case bootstrapPath == "":
		bootstrapCfg, err = config.LoadBootstrap(bootstrapPath)
	case fileExists(bootstrapPath):
		bootstrapCfg, err = config.Load(bootstrapPath)
	default:
		bootstrapCfg, err = config.LoadBootstrap(bootstrapPath)
		if err == nil {
			bootstrapPath = ""
		}
	}
	if err != nil {
		log.Fatalf("load config failed: %v", err)
	}

	store, err := admin.NewStore(bootstrapPath, bootstrapCfg)
	if err != nil {
		log.Fatalf("init admin store failed: %v", err)
	}
	defer store.Close()

	cfg, err := store.GetConfig()
	if err != nil {
		log.Fatalf("load effective config failed: %v", err)
	}
	if generated := store.GeneratedAdminPassword(); generated != "" {
		log.Printf("generated initial admin password for %s: %s", cfg.Admin.Username, generated)
	}

	rawKeyStore, err := keystore.New(bootstrapCfg.Storage.UpstreamKeys)
	if err != nil {
		log.Fatalf("init upstream key store failed: %v", err)
	}

	keyStore, err := keystore.NewAsyncStatusStore(rawKeyStore, keystore.AsyncStatusStoreOptions{
		SetStatusTimeout: time.Second,
	})
	if err != nil {
		_ = rawKeyStore.Close()
		log.Fatalf("init async upstream key store failed: %v", err)
	}
	defer keyStore.Close()

	runtime, err := gateway.NewRuntime(cfg, keyStore)
	if err != nil {
		log.Fatalf("init gateway runtime failed: %v", err)
	}
	defer runtime.Close()
	statsStore, ok := any(keyStore).(keystore.RuntimeStatsStore)
	if !ok {
		log.Fatalf("upstream key store does not support runtime stats persistence")
	}
	statsPersister, err := gateway.NewRuntimeStatsPersister(runtime, statsStore, gateway.RuntimeStatsPersisterOptions{})
	if err != nil {
		log.Fatalf("init runtime stats persister failed: %v", err)
	}
	defer statsPersister.Close()

	sessions := admin.NewSessionManager(cfg.Admin.SessionTTL)
	audit := admin.NewAuditLogger(cfg.Admin.AuditLogPath)
	service := admin.NewService(store, runtime, keyStore, sessions, audit)
	adminHandler := admin.NewHandler(service, sessions)

	mux := http.NewServeMux()
	adminHandler.Register(mux)
	mux.Handle("/", runtime)

	srv := &http.Server{
		Addr:         cfg.Server.Listen,
		Handler:      mux,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}

	if cfg.Server.TLS.Enabled() {
		tlsCfg, err := cfg.Server.TLS.GoTLSConfig()
		if err != nil {
			log.Fatalf("init TLS config failed: %v", err)
		}
		srv.TLSConfig = tlsCfg
	}

	go func() {
		if cfg.Server.TLS.Enabled() {
			log.Printf("jc_proxy listening on %s (HTTPS)", cfg.Server.Listen)
			if err := srv.ListenAndServeTLS(cfg.Server.TLS.CertFile, cfg.Server.TLS.KeyFile); err != nil && err != http.ErrServerClosed {
				log.Fatalf("listen failed: %v", err)
			}
		} else {
			log.Printf("jc_proxy listening on %s", cfg.Server.Listen)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("listen failed: %v", err)
			}
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}
