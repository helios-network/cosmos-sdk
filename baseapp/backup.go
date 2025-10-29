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

func (bm *BackupManager) PerformBackup(height int64) (string, error) {
	rootDir := bm.rootDir
	if rootDir == "" {
		rootDir = "."
	}

	if err := os.MkdirAll(bm.backupDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create backup directory: %w", err)
	}

	return bm.createBackupArchive(rootDir, height, bm.backupDir)
}

func (bm *BackupManager) PerformRestore(fileName string) error {
	return bm.installSnapshot(fileName)
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

func (bm *BackupManager) installSnapshot(fileName string) error {
	snapshotDir := filepath.Join(bm.rootDir, "backups")
	snapshotFile := filepath.Join(snapshotDir, fileName)

	// check if the file exists
	if _, err := os.Stat(snapshotFile); os.IsNotExist(err) {
		return fmt.Errorf("snapshot file does not exist: %s", snapshotFile)
	}

	// Define files to be replaced
	dbFilesToIncludeInSnapshot := []string{"application.db", "blockstore.db", "state.db", "metadata.json"}
	configFilesToIncludeInSnapshot := []string{"genesis.json", "addrbook.json", "persistent_peers.txt"}

	// Remove existing files and directories
	dataDir := filepath.Join(bm.rootDir, "data")
	configDir := filepath.Join(bm.rootDir, "config")

	// Remove database files and directories
	for _, dbFile := range dbFilesToIncludeInSnapshot {
		dbPath := filepath.Join(dataDir, dbFile)
		if err := os.RemoveAll(dbPath); err != nil {
			bm.logger.Error("Failed to remove existing database file", "file", dbPath, "error", err)
		}
	}

	// Remove config files
	for _, configFile := range configFilesToIncludeInSnapshot {
		configPath := filepath.Join(configDir, configFile)
		if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
			bm.logger.Error("Failed to remove existing config file", "file", configPath, "error", err)
		}
	}

	// Open and extract the snapshot
	snapshotFileReader, err := os.Open(snapshotFile)
	if err != nil {
		return fmt.Errorf("failed to open snapshot file: %w", err)
	}
	defer snapshotFileReader.Close()

	// Create gzip reader
	gzipReader, err := gzip.NewReader(snapshotFileReader)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzipReader.Close()

	// Create tar reader
	tarReader := tar.NewReader(gzipReader)

	// Extract files from the archive
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar header: %w", err)
		}

		// Skip if it's a directory
		if header.Typeflag == tar.TypeDir {
			continue
		}

		// Parse the path to determine target location
		// Expected format: backup/data/filename or backup/config/filename
		pathParts := strings.Split(header.Name, "/")
		if len(pathParts) < 3 || pathParts[0] != "backup" {
			bm.logger.Warn("Unexpected file path in snapshot", "path", header.Name)
			continue
		}

		var targetDir string
		var fileName string

		if pathParts[1] == "data" {
			targetDir = dataDir
			fileName = pathParts[2]
		} else if pathParts[1] == "config" {
			targetDir = configDir
			fileName = pathParts[2]
		} else {
			bm.logger.Warn("Unknown directory in snapshot", "directory", pathParts[1])
			continue
		}

		if pathParts[2] == "application.db" {
			targetDir = filepath.Join(targetDir, "application.db")
			fileName = pathParts[3]
		} else if pathParts[2] == "blockstore.db" {
			targetDir = filepath.Join(targetDir, "blockstore.db")
			fileName = pathParts[3]
		} else if pathParts[2] == "state.db" {
			targetDir = filepath.Join(targetDir, "state.db")
			fileName = pathParts[3]
		}

		// create directory if it doesn't exist
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", targetDir, err)
		}

		// Create target file
		targetPath := filepath.Join(targetDir, fileName)
		targetFile, err := os.Create(targetPath)
		if err != nil {
			return fmt.Errorf("failed to create target file %s: %w", targetPath, err)
		}

		// Copy file content
		_, err = io.Copy(targetFile, tarReader)
		targetFile.Close()
		if err != nil {
			return fmt.Errorf("failed to copy file content to %s: %w", targetPath, err)
		}

		bm.logger.Info("Extracted file from snapshot", "file", targetPath)
	}

	// write persistent_peers.txt to config.toml if persistent_peers.txt exists
	if _, err := os.Stat(filepath.Join(configDir, "persistent_peers.txt")); err == nil {
		// apply persistent_peers.txt to config.toml
		persistentPeers, err := os.ReadFile(filepath.Join(configDir, "persistent_peers.txt"))
		if err != nil {
			return fmt.Errorf("failed to read persistent_peers.txt: %w", err)
		}
		configToml, err := os.ReadFile(filepath.Join(configDir, "config.toml"))
		if err != nil {
			return fmt.Errorf("failed to read config.toml: %w", err)
		}
		lines := strings.Split(string(configToml), "\n")
		for i, line := range lines {
			if strings.HasPrefix(line, "persistent_peers =") {
				lines[i] = "persistent_peers = \"" + string(strings.Trim(strings.Trim(string(persistentPeers), " "), "\"")) + "\""
				break
			}
		}
		err = os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(strings.Join(lines, "\n")), 0o644)
		if err != nil {
			return fmt.Errorf("failed to write config.toml: %w", err)
		}
	}

	// write new priv_validator_state.json if not exists
	err = os.WriteFile(filepath.Join(dataDir, "priv_validator_state.json"), []byte("{\"height\":\"0\",\"round\":0,\"step\":0}"), 0o644)
	if err != nil {
		return fmt.Errorf("failed to write priv_validator_state.json: %w", err)
	}

	bm.logger.Info("Successfully installed snapshot", "file", fileName)
	return nil
}

func (bm *BackupManager) createBackupArchive(rootDir string, height int64, backupDir string) (string, error) {
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	tempBackupName := fmt.Sprintf("tmp_snapshot_%d_%s", height, timestamp)
	backupName := fmt.Sprintf("snapshot_%d_%s", height, timestamp)

	configToml, err := os.ReadFile(filepath.Join(rootDir, "config", "config.toml"))
	if err != nil {
		return "", fmt.Errorf("failed to read config.toml: %w", err)
	}
	// retrieve persistent_peers from config.toml
	persistentPeers := ""
	lines := strings.Split(string(configToml), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "persistent_peers =") {
			persistentPeers = strings.TrimPrefix(line, "persistent_peers =")
			break
		}
	}
	// trim quotes
	persistentPeers = strings.Trim(strings.Trim(persistentPeers, " "), "\"")
	err = os.WriteFile(filepath.Join(rootDir, "config", "persistent_peers.txt"), []byte(persistentPeers), 0o644)
	if err != nil {
		return "", fmt.Errorf("failed to write persistent_peers.txt: %w", err)
	}

	dataDir := filepath.Join(rootDir, "data")
	configDir := filepath.Join(rootDir, "config")
	dbFilesToIncludeInSnapshot := []string{"application.db", "blockstore.db", "state.db", "metadata.json"}
	configFilesToIncludeInSnapshot := []string{"genesis.json", "addrbook.json", "persistent_peers.txt"}

	err = bm.deleteOldSnapshots(backupDir, bm.minRetainBackups)
	if err != nil {
		return "", fmt.Errorf("failed to delete old snapshots: %w", err)
	}

	err = bm.createTarGzArchiveOfSelectedFilesAndDirs(filepath.Join(backupDir, tempBackupName+".tar.gz"), []string{dataDir, configDir}, [][]string{dbFilesToIncludeInSnapshot, configFilesToIncludeInSnapshot})
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			time.Sleep(1000 * time.Millisecond)
			bm.logger.Info("Retrying to create snapshot data archive", "error", err)
			return bm.createBackupArchive(rootDir, height, backupDir)
		}
		return "", fmt.Errorf("failed to create snapshot data archive: %w", err)
	}

	// rename temp backup to backup name
	err = os.Rename(filepath.Join(backupDir, tempBackupName+".tar.gz"), filepath.Join(backupDir, backupName+".tar.gz"))
	if err != nil {
		return "", fmt.Errorf("failed to rename temp backup to backup name: %w", err)
	}

	return filepath.Join(backupDir, backupName+".tar.gz"), nil
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

			// get dir of sourceDir
			sourceDirName := filepath.Base(sourceDir)

			// Prefix all paths with "backup/" to ensure tar doesn't create unwanted directories
			relPath = "backup/" + sourceDirName + "/" + relPath

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
			// remove archive file
			os.Remove(archivePath)
			return err
		}
	}

	return nil
}
