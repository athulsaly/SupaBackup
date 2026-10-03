package engine

import (
	"context"
	"fmt"

	"supabackup/internal/config"
)

// MongoEngine is a stub implementation designed to eventually wrap mongodump.
type MongoEngine struct{}

// NewMongoEngine constructs a new MongoEngine stub.
func NewMongoEngine() *MongoEngine {
	return &MongoEngine{}
}

// Backup returns an informative error for the MongoDB stub in the MVP release.
func (m *MongoEngine) Backup(ctx context.Context, job config.JobConfig, targetPath string) error {
	return fmt.Errorf("mongodb engine is not yet implemented in SupaBackup MVP (support planned for future release)")
}

// Verify returns an informative error for the MongoDB stub.
func (m *MongoEngine) Verify(ctx context.Context, targetPath string) error {
	return fmt.Errorf("mongodb verification is not yet implemented in SupaBackup MVP")
}
