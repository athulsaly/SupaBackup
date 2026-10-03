package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"supabackup/internal/config"
	"supabackup/internal/health"
	"supabackup/internal/notify"
	"supabackup/internal/scheduler"
	"supabackup/internal/web"
)

var (
	version = "0.2.0"
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

	logBuf := health.NewLogBuffer(100)
	notifier := notify.NewNotifier(nil)
	sched := scheduler.NewScheduler(cfg, tracker, notifier, nil)
	sched.SetLogBuffer(logBuf)

	if err := sched.Start(); err != nil {
		log.Fatalf("[supabackup] fatal: failed to start scheduler: %v", err)
	}

	var webServer *web.Server
	dashPass, err := config.ResolveSecret("DASHBOARD_PASSWORD")
	if err != nil {
		log.Printf("[web] warning resolving DASHBOARD_PASSWORD: %v", err)
	}
	if dashPass != "" {
		sc := &web.ServerContext{
			StartTime:  time.Now(),
			Config:     cfg,
			Tracker:    tracker,
			Scheduler:  sched,
			LogBuffer:  logBuf,
			Auth:       web.NewAuthManager(dashPass),
			BackupDir:  cfg.BackupDir,
			IsWritable: os.Getenv("DASHBOARD_CONFIG_WRITABLE") == "true",
		}
		webServer = web.NewServer(sc, os.Getenv("DASHBOARD_PORT"))
		go func() {
			if err := webServer.Start(); err != nil {
				log.Printf("[web] error: %v", err)
			}
		}()
	} else {
		log.Printf("[web] DASHBOARD_PASSWORD not set; web dashboard disabled")
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	sig := <-sigChan
	log.Printf("[supabackup] received signal %s, initiating graceful shutdown...", sig)

	if webServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = webServer.Stop(shutdownCtx)
	}

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
