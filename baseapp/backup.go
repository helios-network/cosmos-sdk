package baseapp

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"cosmossdk.io/log"
)

type BackupManager struct {
	enabled          bool
	blockInterval    uint64
	backupDir        string
	rootDir          string
	minRetainBlocks  uint64
	minRetainBackups uint64
	cms              interface{}
	logger           log.Logger
}

func NewBackupManager(logger log.Logger) *BackupManager {
	return &BackupManager{
		logger: logger,
	}
}

func (bm *BackupManager) Configure(enabled bool, blockInterval uint64, backupDir string, rootDir string, minRetainBlocks uint64, minRetainBackups uint64, cms interface{}) error {
	if enabled {
		if err := bm.validateConfiguration(blockInterval, minRetainBlocks, cms); err != nil {
			bm.logger.Error("Backup system disabled", "error", err)
			bm.enabled = false
			return err
		}
	}

	bm.enabled = enabled
	bm.blockInterval = blockInterval
	if backupDir != "" {
		bm.backupDir = filepath.Join(rootDir, backupDir)
	} else {
		bm.backupDir = filepath.Join(rootDir, "backups")
	}
	bm.rootDir = rootDir
	bm.minRetainBlocks = minRetainBlocks
	bm.minRetainBackups = minRetainBackups
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

func (bm *BackupManager) GetMinRetainBackups() uint64 {
	return bm.minRetainBackups
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

	return nil
}

func (bm *BackupManager) createBackupArchive(rootDir string, height int64, backupDir string) error {
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	backupName := fmt.Sprintf("snapshot_%d_%s", height, timestamp)

	dataDir := filepath.Join(rootDir, "data")
	configDir := filepath.Join(rootDir, "config")
	dbFilesToIncludeInSnapshot := []string{"application.db", "blockstore.db", "state.db"}
	configFilesToIncludeInSnapshot := []string{"genesis.json"}

	err := bm.deleteOldSnapshots(backupDir, bm.minRetainBackups)
	if err != nil {
		return fmt.Errorf("failed to delete old snapshots: %w", err)
	}

	err = bm.createTarGzArchiveOfSelectedFilesAndDirs(filepath.Join(backupDir, backupName+".tar.gz"), []string{dataDir, configDir}, [][]string{dbFilesToIncludeInSnapshot, configFilesToIncludeInSnapshot})
	if err != nil {
		return fmt.Errorf("failed to create snapshot data archive: %w", err)
	}

	return nil
}

func (bm *BackupManager) deleteOldSnapshots(backupDir string, minRetainBackups uint64) error {
	files, err := bm.listSnapshotFiles(backupDir)
	if err != nil {
		return fmt.Errorf("failed to list snapshot files: %w", err)
	}

	if len(files) <= int(minRetainBackups) {
		return nil
	}

	filesToDelete := files[:len(files)-int(minRetainBackups)]

	for _, file := range filesToDelete {
		if err := os.Remove(filepath.Join(backupDir, file.Name)); err != nil {
			return fmt.Errorf("failed to delete snapshot file: %w", err)
		}
		bm.logger.Info("Deleted old backup", "file", file.Name, "height", file.Height)
	}

	return nil
}

type SnapshotFile struct {
	Name   string
	Height int64
}

func (bm *BackupManager) listSnapshotFiles(backupDir string) ([]SnapshotFile, error) {
	files, err := os.ReadDir(backupDir)
	if err != nil {
		return nil, err
	}
	filesNames := make([]string, 0)
	for _, file := range files {
		if strings.HasPrefix(file.Name(), "snapshot_") && strings.HasSuffix(file.Name(), ".tar.gz") {
			// test if the file contains a valid height
			parts := strings.Split(file.Name(), "_")
			if len(parts) < 3 {
				continue
			}
			_, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				continue
			}
			filesNames = append(filesNames, file.Name())
		}
	}
	if len(filesNames) > 1 {
		sort.Slice(filesNames, func(i, j int) bool {
			partsI := strings.Split(filesNames[i], "_")
			partsJ := strings.Split(filesNames[j], "_")
			heightI, _ := strconv.ParseInt(partsI[1], 10, 64)
			heightJ, _ := strconv.ParseInt(partsJ[1], 10, 64)
			return heightI < heightJ
		})
	}
	snapshotFiles := make([]SnapshotFile, len(filesNames))
	for i, file := range filesNames {
		parts := strings.Split(file, "_")
		height, _ := strconv.ParseInt(parts[1], 10, 64)
		snapshotFiles[i] = SnapshotFile{Name: file, Height: height}
	}
	return snapshotFiles, nil
}

func (bm *BackupManager) createTarGzArchiveOfSelectedFilesAndDirs(archivePath string, sourceDirs []string, filesAndDirsToInclude [][]string) error {
	archiveFile, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	defer archiveFile.Close()

	gzipWriter := gzip.NewWriter(archiveFile)
	defer gzipWriter.Close()

	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	for i, sourceDir := range sourceDirs {
		err := filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}

			// Check if the path is in the list of files and directories to include
			found := false
			for _, fileOrDir := range filesAndDirsToInclude[i] {
				if strings.Contains(path, fileOrDir) {
					found = true
					break
				}
			}
			if !found {
				return nil
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

		if err != nil {
			return err
		}
	}

	return nil
}
