package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/faddey/xray-monitor/internal/api"
	"github.com/faddey/xray-monitor/internal/config"
	"github.com/faddey/xray-monitor/internal/install"
	"github.com/faddey/xray-monitor/internal/monitor"
	"github.com/faddey/xray-monitor/internal/store"
	xraycore "github.com/faddey/xray-monitor/internal/xray"
)

var version = "dev"

func main() {
	if err := execute(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func execute(args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "run":
		flags := flag.NewFlagSet("run", flag.ContinueOnError)
		configPath := flags.String("config", "/etc/xray-monitor/config.json", "path to JSON configuration")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		return run(*configPath)
	case "validate":
		flags := flag.NewFlagSet("validate", flag.ContinueOnError)
		configPath := flags.String("config", "/etc/xray-monitor/config.json", "path to JSON configuration")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		_, err := config.Load(*configPath)
		return err
	case "install":
		flags := flag.NewFlagSet("install", flag.ContinueOnError)
		var subscriptions stringList
		flags.Var(&subscriptions, "subscription", "subscription URL (repeat for multiple subscriptions)")
		publicHost := flags.String("public-host", "", "hostname or IP used in the printed API URL (default: detected public IPv4)")
		xrayBinary := flags.String("xray-binary", "xray", "path to the Xray executable")
		force := flags.Bool("force", false, "replace an existing installation")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		result, err := install.Run(install.Options{
			Subscriptions: subscriptions,
			PublicHost:    *publicHost,
			XrayBinary:    *xrayBinary,
			Force:         *force,
		})
		if err != nil {
			return err
		}
		fmt.Println("Service installed and started.")
		fmt.Println("Status URL:", result.URL)
		fmt.Println("Bearer token:", result.Token)
		fmt.Printf("Example: curl -H 'Authorization: Bearer %s' '%s'\n", result.Token, result.URL)
		return nil
	case "version", "--version", "-version":
		fmt.Println(version)
		return nil
	default:
		return usageError()
	}
}

func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if err := cfg.EnsureDirectories(); err != nil {
		return fmt.Errorf("create data directories: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	manager := xraycore.NewManager(cfg.XrayBinary, cfg.XrayWorkDir, logger)
	if err := manager.CheckBinary(); err != nil {
		return err
	}
	database, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()
	mon := monitor.New(cfg, database, manager, logger)
	server := api.New(cfg.Listen, cfg.APIToken, mon, database, logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	defer manager.Stop()
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		mon.Run(ctx)
	}()
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("API listening", "address", cfg.Listen)
		serverErr <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		if err := server.Shutdown(); err != nil {
			logger.Error("HTTP shutdown", "error", err)
		}
		<-monitorDone
		return nil
	case err := <-serverErr:
		stop()
		<-monitorDone
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func usageError() error {
	return errors.New("usage: xray-monitor <run|validate|install|version> [options]")
}

type stringList []string

func (s *stringList) String() string { return fmt.Sprint([]string(*s)) }
func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}
