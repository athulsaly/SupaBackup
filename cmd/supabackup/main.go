package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"supabackup/internal/config"
	"supabackup/internal/health"
	"supabackup/internal/notify"
	"supabackup/internal/scheduler"
)

var (
	version = "0.1.0-mvp"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "check", "health":
			runHealthCheck(os.Args[2:])
			return
		case "run-now":
			runNow(os.Args[2:])
			return
		case "version", "-v", "--version":
			fmt.Printf("SupaBackup version %s\n", version)
			return
		}
	}

	runDaemon()
}

func runDaemon() {
	log.Printf("[supabackup] starting SupaBackup daemon (version: %s)", version)

	cfgPath := os.Getenv("CONFIG_PATH")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("[supabackup] fatal: configuration load error: %v", err)
	}

	log.Printf("[supabackup] loaded %d job(s) from %q (backup_dir: %q, status_file: %q)",
		len(cfg.Jobs), cfg.ConfigPath, cfg.BackupDir, cfg.StatusFile)

	tracker, err := health.NewTracker(cfg.StatusFile, cfg.Jobs)
	if err != nil {
		log.Fatalf("[supabackup] fatal: status tracker initialization failed: %v", err)
	}

	notifier := notify.NewNotifier(nil)
	sched := scheduler.NewScheduler(cfg, tracker, notifier, nil)

	if err := sched.Start(); err != nil {
		log.Fatalf("[supabackup] fatal: failed to start scheduler: %v", err)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	sig := <-sigChan
	log.Printf("[supabackup] received signal %s, initiating graceful shutdown...", sig)

	_ = sched.Stop()
	log.Printf("[supabackup] shutdown complete")
}

func runHealthCheck(args []string) {
	fs := flag.NewFlagSet("health", flag.ExitOnError)
	statusFile := fs.String("status-file", "", "path to status JSON file")
	maxFailures := fs.Int("max-failures", 3, "maximum consecutive failures before reporting unhealthy")
	_ = fs.Parse(args)

	targetFile := *statusFile
	if targetFile == "" {
		targetFile = os.Getenv("STATUS_FILE")
	}
	if targetFile == "" {
		targetFile = config.DefaultStatusFile
	}

	if err := health.CheckHealth(targetFile, *maxFailures); err != nil {
		fmt.Fprintf(os.Stderr, "HEALTHCHECK FAIL: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("HEALTHCHECK OK")
	os.Exit(0)
}

func runNow(args []string) {
	fs := flag.NewFlagSet("run-now", flag.ExitOnError)
	jobName := fs.String("job", "", "name of the specific job to run (empty runs all jobs)")
	_ = fs.Parse(args)

	cfgPath := os.Getenv("CONFIG_PATH")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("[supabackup] fatal: configuration load error: %v", err)
	}

	tracker, err := health.NewTracker(cfg.StatusFile, cfg.Jobs)
	if err != nil {
		log.Fatalf("[supabackup] fatal: tracker error: %v", err)
	}

	sched := scheduler.NewScheduler(cfg, tracker, notify.NewNotifier(nil), nil)
	ctx := context.Background()

	found := false
	for _, job := range cfg.Jobs {
		if *jobName == "" || job.Name == *jobName {
			found = true
			if err := sched.RunJob(ctx, job); err != nil {
				log.Fatalf("[supabackup] run-now failed for job %q: %v", job.Name, err)
			}
		}
	}

	if !found && *jobName != "" {
		log.Fatalf("[supabackup] job %q not found in configuration", *jobName)
	}
}
