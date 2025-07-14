package baseapp

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"cosmossdk.io/log"
)

type BackupManager struct {
	enabled         bool
	blockInterval   uint64
	backupDir       string
	rootDir         string
	minRetainBlocks uint64
	cms             interface{}
	logger          log.Logger
}

func NewBackupManager(logger log.Logger) *BackupManager {
	return &BackupManager{
		logger: logger,
	}
}

func (bm *BackupManager) Configure(enabled bool, blockInterval uint64, backupDir string, rootDir string, minRetainBlocks uint64, cms interface{}) error {
	if enabled {
		if err := bm.validateConfiguration(blockInterval, minRetainBlocks, cms); err != nil {
			bm.logger.Error("Backup system disabled", "error", err)
			bm.enabled = false
			return err
		}
	}

	bm.enabled = enabled
	bm.blockInterval = blockInterval
	bm.backupDir = backupDir
	bm.rootDir = rootDir
	bm.minRetainBlocks = minRetainBlocks
	bm.cms = cms

	return nil
}

func (bm *BackupManager) ShouldBackup(height int64) bool {
	return bm.enabled && height%int64(bm.blockInterval) == 0
}

func (bm *BackupManager) IsEnabled() bool {
	return bm.enabled
}

func (bm *BackupManager) GetBlockInterval() uint64 {
	return bm.blockInterval
}

func (bm *BackupManager) GetBackupDir() string {
	return bm.backupDir
}

func (bm *BackupManager) PerformBackup(height int64) error {
	if bm.cms == nil {
		return fmt.Errorf("CommitMultiStore is not initialized")
	}

	rootDir := bm.rootDir
	if rootDir == "" {
		rootDir = "."
	}

	if err := os.MkdirAll(bm.backupDir, 0o755); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	return bm.createBackupArchive(rootDir, height, bm.backupDir)
}

func (bm *BackupManager) validateConfiguration(blockInterval, minRetainBlocks uint64, cms interface{}) error {
	if blockInterval == 0 {
		return fmt.Errorf("backup interval must be > 0")
	}

	if minRetainBlocks == 0 {
		return fmt.Errorf("minRetainBlocks must be > 0 (current: %d)", minRetainBlocks)
	}

	if minRetainBlocks < 10 {
		return fmt.Errorf("minRetainBlocks too low (current: %d, minimum: 10)", minRetainBlocks)
	}

	if cms == nil {
		return fmt.Errorf("CommitMultiStore not initialized")
	}

	if blockInterval > minRetainBlocks/2 {
		return fmt.Errorf("backup interval (%d) too high compared to retention (%d)", blockInterval, minRetainBlocks)
	}

	return nil
}

func (bm *BackupManager) createBackupArchive(rootDir string, height int64, backupDir string) error {
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	backupName := fmt.Sprintf("snapshot_%d_%s", height, timestamp)

	snapshotDir := filepath.Join(backupDir, "snapshot_data")
	if err := os.RemoveAll(snapshotDir); err != nil {
		return fmt.Errorf("failed to remove existing snapshot directory: %w", err)
	}

	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		return fmt.Errorf("failed to create snapshot directory: %w", err)
	}

	dataDir := filepath.Join(rootDir, "data")
	dbFiles := []string{"application.db", "blockstore.db", "state.db"}
	copiedFiles := 0

	for _, dbFile := range dbFiles {
		sourcePath := filepath.Join(dataDir, dbFile)
		destPath := filepath.Join(snapshotDir, dbFile)

		if err := bm.copyFileOrDir(sourcePath, destPath); err != nil {
			bm.logger.Warn("Failed to copy database file", "file", dbFile, "error", err)
			continue
		}
		copiedFiles++
	}

	if copiedFiles == 0 {
		return fmt.Errorf("no database files were successfully copied")
	}

	if err := bm.createPrivValidatorStateFile(snapshotDir); err != nil {
		return fmt.Errorf("failed to create priv_validator_state.json: %w", err)
	}

	archivePath := filepath.Join(backupDir, backupName+".tar.gz")
	if err := bm.createTarGzArchive(snapshotDir, archivePath); err != nil {
		return fmt.Errorf("failed to create archive: %w", err)
	}

	os.RemoveAll(snapshotDir)

	return nil
}

func (bm *BackupManager) copyFileOrDir(src, dest string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	if srcInfo.IsDir() {
		return bm.copyDirectory(src, dest)
	}

	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

func (bm *BackupManager) copyDirectory(src, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		destPath := filepath.Join(dest, entry.Name())

		if entry.IsDir() {
			if err := bm.copyDirectory(srcPath, destPath); err != nil {
				return err
			}
		} else {
			if err := bm.copyFile(srcPath, destPath); err != nil {
				return err
			}
		}
	}

	return nil
}

func (bm *BackupManager) copyFile(src, dest string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

func (bm *BackupManager) createPrivValidatorStateFile(snapshotDir string) error {
	privValidatorState := map[string]interface{}{
		"height": "0",
		"round":  0,
		"step":   0,
	}

	jsonData, err := json.MarshalIndent(privValidatorState, "", "  ")
	if err != nil {
		return err
	}

	filePath := filepath.Join(snapshotDir, "priv_validator_state.json")
	return os.WriteFile(filePath, jsonData, 0o644)
}

func (bm *BackupManager) createTarGzArchive(sourceDir, archivePath string) error {
	archiveFile, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	defer archiveFile.Close()

	gzipWriter := gzip.NewWriter(archiveFile)
	defer gzipWriter.Close()

	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	return filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		header.Name = relPath

		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}

		if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}

			_, copyErr := io.Copy(tarWriter, file)
			file.Close()
			if copyErr != nil {
				return copyErr
			}
		}

		return nil
	})
}
