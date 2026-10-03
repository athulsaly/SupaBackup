package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"supabackup/internal/config"
)

func TestEngineGet(t *testing.T) {
	tests := []struct {
		name        string
		expectError bool
	}{
		{"postgres", false},
		{"mysql", false},
		{"mongo", false},
		{"redis", true},
	}

	for _, tc := range tests {
		eng, err := Get(tc.name)
		if tc.expectError && err == nil {
			t.Errorf("expected error for engine %q, got nil", tc.name)
		}
		if !tc.expectError && (err != nil || eng == nil) {
			t.Errorf("expected valid engine for %q, got error: %v", tc.name, err)
		}
	}
}

func TestCheckFileSize(t *testing.T) {
	tempDir := t.TempDir()

	missingFile := filepath.Join(tempDir, "missing.dump")
	if err := CheckFileSize(missingFile); err == nil {
		t.Errorf("expected error for missing file, got nil")
	}

	emptyFile := filepath.Join(tempDir, "empty.dump")
	if err := os.WriteFile(emptyFile, []byte{}, 0644); err != nil {
		t.Fatalf("failed to write empty file: %v", err)
	}
	if err := CheckFileSize(emptyFile); err == nil {
		t.Errorf("expected error for empty file, got nil")
	}

	validFile := filepath.Join(tempDir, "valid.dump")
	if err := os.WriteFile(validFile, []byte("data"), 0644); err != nil {
		t.Fatalf("failed to write valid file: %v", err)
	}
	if err := CheckFileSize(validFile); err != nil {
		t.Errorf("expected valid file to pass, got error: %v", err)
	}
}

func TestPostgresEngine_Backup(t *testing.T) {
	var executedName string
	var executedArgs []string
	var executedCmd *exec.Cmd

	mockRunner := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		executedName = name
		executedArgs = args
		executedCmd = exec.Command("true")
		return executedCmd
	}

	p := NewPostgresEngine(mockRunner)
	job := config.JobConfig{
		Name:           "supa",
		Engine:         "postgres",
		URL:            "postgresql://postgres:secretpassword@localhost:5432/postgres",
		ExcludeSchemas: []string{"supabase_functions"},
	}

	targetPath := "/tmp/test.dump"
	err := p.Backup(context.Background(), job, targetPath)
	if err != nil {
		t.Fatalf("unexpected backup error: %v", err)
	}

	if executedName != "pg_dump" {
		t.Errorf("expected executable pg_dump, got %q", executedName)
	}

	joinedArgs := strings.Join(executedArgs, " ")
	// Ensure password is NEVER in command-line arguments
	if strings.Contains(joinedArgs, "secretpassword") {
		t.Errorf("secret leaked in process arguments: %s", joinedArgs)
	}

	if !strings.Contains(joinedArgs, "--format=custom") {
		t.Errorf("expected --format=custom in args, got: %s", joinedArgs)
	}
	if !strings.Contains(joinedArgs, "--file="+targetPath) {
		t.Errorf("expected --file in args, got: %s", joinedArgs)
	}
	if !strings.Contains(joinedArgs, "-h localhost") {
		t.Errorf("expected -h localhost in args, got: %s", joinedArgs)
	}
	if !strings.Contains(joinedArgs, "-p 5432") {
		t.Errorf("expected -p 5432 in args, got: %s", joinedArgs)
	}
	if !strings.Contains(joinedArgs, "-U postgres") {
		t.Errorf("expected -U postgres in args, got: %s", joinedArgs)
	}
	if !strings.Contains(joinedArgs, "-d postgres") {
		t.Errorf("expected -d postgres in args, got: %s", joinedArgs)
	}
	if !strings.Contains(joinedArgs, "--exclude-schema=supabase_functions") {
		t.Errorf("expected --exclude-schema in args, got: %s", joinedArgs)
	}

	// Verify password is passed via PGPASSWORD environment variable
	foundPGPassword := false
	for _, env := range executedCmd.Env {
		if env == "PGPASSWORD=secretpassword" {
			foundPGPassword = true
			break
		}
	}
	if !foundPGPassword {
		t.Errorf("expected PGPASSWORD in cmd.Env, got: %+v", executedCmd.Env)
	}
}

func TestPostgresEngine_Verify(t *testing.T) {
	tempDir := t.TempDir()
	dumpFile := filepath.Join(tempDir, "test.dump")
	if err := os.WriteFile(dumpFile, []byte("PGDMP...dummy-content"), 0644); err != nil {
		t.Fatalf("failed to write dump file: %v", err)
	}

	var executedName string
	var executedArgs []string
	mockRunner := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		executedName = name
		executedArgs = args
		return exec.Command("true")
	}

	p := NewPostgresEngine(mockRunner)
	err := p.Verify(context.Background(), dumpFile)
	if err != nil {
		t.Fatalf("unexpected verify error: %v", err)
	}

	if executedName != "pg_restore" {
		t.Errorf("expected pg_restore, got %q", executedName)
	}
	if len(executedArgs) != 2 || executedArgs[0] != "--list" || executedArgs[1] != dumpFile {
		t.Errorf("unexpected args for pg_restore: %+v", executedArgs)
	}
}

func TestMySQLEngine_Backup(t *testing.T) {
	var executedName string
	var executedArgs []string
	var executedCmd *exec.Cmd
	mockRunner := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		executedName = name
		executedArgs = args
		executedCmd = exec.Command("true")
		return executedCmd
	}

	m := NewMySQLEngine(mockRunner)
	job := config.JobConfig{
		Name:   "mysql-app",
		Engine: "mysql",
		URL:    "mysql://dbuser:mypassword@mysql.internal:3307/appdb",
	}

	targetPath := "/tmp/mysql.dump"
	err := m.Backup(context.Background(), job, targetPath)
	if err != nil {
		t.Fatalf("unexpected backup error: %v", err)
	}

	if executedName != "mysqldump" {
		t.Errorf("expected mysqldump, got %q", executedName)
	}

	joinedArgs := strings.Join(executedArgs, " ")
	if !strings.Contains(joinedArgs, "-h mysql.internal") {
		t.Errorf("expected host in args, got: %s", joinedArgs)
	}
	if !strings.Contains(joinedArgs, "-P 3307") {
		t.Errorf("expected port in args, got: %s", joinedArgs)
	}
	if !strings.Contains(joinedArgs, "-u dbuser") {
		t.Errorf("expected user in args, got: %s", joinedArgs)
	}
	if !strings.Contains(joinedArgs, "--result-file="+targetPath) {
		t.Errorf("expected result-file in args, got: %s", joinedArgs)
	}
	if !strings.HasSuffix(joinedArgs, "appdb") {
		t.Errorf("expected dbName as last arg, got: %s", joinedArgs)
	}

	foundPwd := false
	for _, env := range executedCmd.Env {
		if env == "MYSQL_PWD=mypassword" {
			foundPwd = true
			break
		}
	}
	if !foundPwd {
		t.Errorf("expected MYSQL_PWD in cmd.Env, got: %+v", executedCmd.Env)
	}
}

func TestMySQLEngine_Verify(t *testing.T) {
	tempDir := t.TempDir()
	validDump := filepath.Join(tempDir, "mysql_valid.dump")
	content := "CREATE TABLE users;\n-- Dump completed on 2026-10-03\n"
	if err := os.WriteFile(validDump, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	m := NewMySQLEngine(nil)
	if err := m.Verify(context.Background(), validDump); err != nil {
		t.Errorf("expected valid MySQL dump to pass, got: %v", err)
	}

	invalidDump := filepath.Join(tempDir, "mysql_invalid.dump")
	if err := os.WriteFile(invalidDump, []byte("random garbage data without any sql markers"), 0644); err != nil {
		t.Fatalf("failed to write invalid file: %v", err)
	}
	if err := m.Verify(context.Background(), invalidDump); err == nil {
		t.Errorf("expected invalid dump to fail verification, got nil")
	}
}

func TestMongoEngine_Stub(t *testing.T) {
	m := NewMongoEngine()
	job := config.JobConfig{Name: "mongo-test", Engine: "mongo"}
	if err := m.Backup(context.Background(), job, "/tmp/m.dump"); err == nil {
		t.Errorf("expected stub error on Backup, got nil")
	}
	if err := m.Verify(context.Background(), "/tmp/m.dump"); err == nil {
		t.Errorf("expected stub error on Verify, got nil")
	}
}
