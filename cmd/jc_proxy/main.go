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

	keyStore, err := keystore.New(bootstrapCfg.Storage.UpstreamKeys)
	if err != nil {
		log.Fatalf("init upstream key store failed: %v", err)
	}
	defer keyStore.Close()

	migrated, err := keystore.BootstrapLegacyKeys(keyStore, bootstrapCfg, cfg)
	if err != nil {
		log.Fatalf("bootstrap upstream keys failed: %v", err)
	}
	if migrated > 0 || bootstrapCfg.HasLegacyUpstreamKeys() || cfg.HasLegacyUpstreamKeys() {
		if err := store.UpdateConfig(cfg); err != nil {
			log.Printf("strip legacy upstream keys from config failed: %v", err)
		} else {
			log.Printf("migrated %d legacy upstream key(s) to external store", migrated)
		}
	}

	runtime, err := gateway.NewRuntime(cfg, keyStore)
	if err != nil {
		log.Fatalf("init gateway runtime failed: %v", err)
	}

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

	go func() {
		log.Printf("jc_proxy listening on %s", cfg.Server.Listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen failed: %v", err)
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
