package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jimyag/e2b-github-runner/internal/config"
	"github.com/jimyag/e2b-github-runner/internal/github"
	"github.com/jimyag/e2b-github-runner/internal/sandboxrunner"
	"github.com/jimyag/e2b-github-runner/internal/server"
	"github.com/jimyag/e2b-github-runner/internal/state"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}
	store := state.New(cfg.StateDir)
	if err := store.Ensure(); err != nil {
		logger.Error("ensure state dir", "error", err)
		os.Exit(1)
	}
	githubHTTPClient := &http.Client{Timeout: 30 * time.Second}
	sandboxHTTPClient := &http.Client{Timeout: cfg.SandboxAPITimeout}
	gh := github.NewClient(cfg.GitHubAPIBaseURL, cfg.GitHubToken, cfg.RunnerScope, cfg.GitHubOwner, cfg.GitHubOrg, cfg.GitHubRepo, githubHTTPClient)
	sb, err := sandboxrunner.NewE2BService(cfg.E2BAPIKey, cfg.E2BAPIURL, sandboxHTTPClient)
	if err != nil {
		logger.Error("create sandbox client", "error", err)
		os.Exit(1)
	}
	handler := server.New(cfg, store, gh, sb, logger)
	recoveryCtx, cancel := context.WithTimeout(context.Background(), cfg.RecoveryTimeout)
	if err := handler.Recover(recoveryCtx); err != nil {
		cancel()
		logger.Error("recover runner state", "error", err)
		os.Exit(1)
	}
	cancel()
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       cfg.HTTPReadTimeout,
		WriteTimeout:      cfg.HTTPWriteTimeout,
		IdleTimeout:       cfg.HTTPIdleTimeout,
		MaxHeaderBytes:    1 << 20,
	}
	logger.Info("starting server", "addr", cfg.HTTPAddr, "state_dir", cfg.StateDir)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
