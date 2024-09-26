package main

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"hash"
	"io"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	progressbar "github.com/schollz/progressbar/v3"
	"github.com/sirupsen/logrus"
)

// Constants
const BLOCKSIZE = 128_000

// Configurations
var shuffleBytes bool
var continuous bool
var threads int
var guard chan struct{}
var wg sync.WaitGroup
var finished bool

// Runtime globals
var log *logrus.Logger
var done = make(chan os.Signal, 1)

// Completed files
type Completed struct {
	CompletedFiles  []string `json:"completed_files"`
	CompletedInodes []uint64 `json:"completed_inodes"`
}

var completed = Completed{}

func saveCompleted() {
	data, _ := json.MarshalIndent(completed, "", " ")
	if err := os.WriteFile("progress.json", data, 0644); err != nil {
		panic(err)
	}
}

func readCompleted() {
	if bytes, err := ioutil.ReadFile("progress.json"); err == nil {
		if err := json.Unmarshal(bytes, &completed); err != nil {
			panic(err)
		}
	}
}

func createBackupFile(path string, info os.FileInfo) (hash.Hash, error) {
	// Prepare backup path
	backupPath := fmt.Sprintf("%s.bak", path)

	// Open backup file as readwrite
	backupFile, err := os.Create(backupPath)
	if err != nil {
		return nil, err
	}
	defer backupFile.Close()

	// Open file
	file, err := os.OpenFile(path, os.O_RDWR, info.Mode().Perm())
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Prepare progressbar
	log.Infof("Backing up file '%s'", path)
	bar := progressbar.DefaultBytes(info.Size(), "backing up")
	defer func() {
		if err := bar.Finish(); err != nil {
			log.Error(err)
		}
	}()

	// Copy to backup file while calculating hash
	hash := sha256.New()
	if _, err = io.Copy(io.MultiWriter(backupFile, bar, hash), file); err != nil {
		return nil, err
	}

	// Return hash with no error
	return hash, nil
}

func deleteBackupFile(path string) (err error) {
	// Prepare backup path
	backupPath := fmt.Sprintf("%s.bak", path)

	// Remove backup path if exists
	if _, err = os.Stat(backupPath); err == nil {
		if err = os.Remove(backupPath); err != nil {
			return err
		}
	}

	// No error
	return nil
}

func restoreBackupFile(path string, info os.FileInfo) error {
	// Prepare backup path
	backupPath := fmt.Sprintf("%s.bak", path)

	// Open backup file as readwrite
	backupFile, err := os.OpenFile(backupPath, os.O_RDWR, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer backupFile.Close()

	// Open file
	file, err := os.OpenFile(path, os.O_RDWR, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer file.Close()

	// Prepare progressbar
	log.Infof("Backing up file '%s'", path)
	bar := progressbar.DefaultBytes(info.Size(), "backing up")
	defer func() {
		if err := bar.Finish(); err != nil {
			log.Error(err)
		}
	}()

	// Copy to original file
	if _, err = io.Copy(io.MultiWriter(file, bar), backupFile); err != nil {
		return err
	}

	// Return with no error
	return nil
}

func RewriteFile(path string, info os.FileInfo, shuffle bool) (hash.Hash, error) {
	// Open file
	file, err := os.OpenFile(path, os.O_RDWR, info.Mode().Perm())
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Prepare progress bar
	bar := progressbar.DefaultBytes(info.Size(), "rewriting")

	// Loop through whole file in BLOCKSIZE chunks
	hash := sha256.New()
	for i := int64(0); i < info.Size(); i += BLOCKSIZE {
		// Prepare buffer
		buf := make([]byte, BLOCKSIZE)

		// Read BLOCKSIZE bytes at offset
		n, err := file.ReadAt(buf, i)
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("failed to read to buf: %v", err)
		}
		buf = buf[:n]

		// Swap bytes if specified and able
		if shuffle && n > 2 {
			buf[0], buf[1] = buf[1], buf[0]
		}

		// Write BLOCKSIZE bytes back at offset
		if _, err := file.WriteAt(buf, i); err != nil {
			return nil, err
		}

		// Add to hash
		hash.Write(buf)

		// Propagate progress bar
		if err = bar.Add(BLOCKSIZE); err != nil {
			if err = bar.Finish(); err != nil {
				panic(err) // Really shouldn't ever reach this point
			}
		}
	}

	// Set modified time back
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		log.Errorf("Failed to set modified time for file '%s'", path)
	}

	// Return hash with no error
	return hash, nil
}

func ShuffleRewriteFile(path string, info os.FileInfo) (err error) {
	// Backup file
	var oldHash hash.Hash
	if oldHash, err = createBackupFile(path, info); err != nil {
		return err
	}

	// Loop twice
	var newHash hash.Hash
	for n := 0; n < 2; n++ {
		if newHash, err = RewriteFile(path, info, true); err != nil {
			return err
		}
	}

	// If for some reason, hashes are not the same, restore backup
	oldHashString := fmt.Sprintf("%x", oldHash.Sum(nil))
	newHashString := fmt.Sprintf("%x", newHash.Sum(nil))
	if oldHashString != newHashString {
		if err := restoreBackupFile(path, info); err != nil {
			log.Errorf("failed to restore backup: %v", err)
		}
		return fmt.Errorf("rewrite failed, hash mismatch '%s' != '%s'", oldHashString, newHashString)
	}

	// Delete backup file
	if err := deleteBackupFile(path); err != nil {
		log.Errorf("failed to delete backup file: %v", err)
	}

	// Return no error
	return nil
}

func IsCompleted(path string, inode uint64) bool {
	for _, b := range completed.CompletedFiles {
		if b == path {
			log.Infof("Skipping file '%s'\n", path)

			// Check if inode exists
			inodeExists := false
			for _, i := range completed.CompletedInodes {
				if i == inode {
					inodeExists = true
					break
				}
			}

			// If not in CompletedInodes, add to it
			if !inodeExists {
				completed.CompletedInodes = append(completed.CompletedInodes, inode)
			}

			// Return early
			return false
		}
	}

	for _, b := range completed.CompletedInodes {
		if b == inode {
			log.Infof("Skipping inode '%d'\n", inode)

			// Check if path exists
			pathExists := false
			for _, i := range completed.CompletedFiles {
				if i == path {
					pathExists = true
					break
				}
			}

			// If not in CompletedFiles, add to it
			if !pathExists {
				completed.CompletedFiles = append(completed.CompletedFiles, path)
			}

			// Return early
			return false
		}
	}

	return true
}

func Rewrite(path string, info os.FileInfo, err error) error {
	// Call lstat() if info is nil, return if error
	if info == nil {
		info, err = os.Lstat(path)
		if err != nil {
			return err
		}
	}

	// Return early if not file
	if info.IsDir() {
		return nil
	}

	// Get file inode
	stat, _ := info.Sys().(*syscall.Stat_t)
	inode := stat.Ino

	// Return early if error
	if err != nil {
		return err
	}

	// Return early if already completed and not continuously rewriting
	if !continuous && !IsCompleted(path, inode) {
		return nil
	}

	// Rewrite file
	if shuffleBytes {
		if err := ShuffleRewriteFile(path, info); err != nil {
			return err
		}
	} else {
		if _, err := RewriteFile(path, info, false); err != nil {
			return err
		}
	}

	// Log
	log.Infof("Rewritten file '%s'", path)

	// Save progress
	completed.CompletedFiles = append(completed.CompletedFiles, path)
	if stat != nil {
		completed.CompletedInodes = append(completed.CompletedInodes, stat.Ino)
	}
	saveCompleted()

	// Return nil
	return nil
}

func RewriteRouting(path string, info os.FileInfo, err error) error {
	// Start goroutine
	guard <- struct{}{}
	wg.Add(1)
	go func() {
		err = Rewrite(path, info, err)
		if err != nil {
			log.Errorf("Rewrite failed: %+v", err)
		}
		wg.Done()
		<-guard
	}()

	// Return error if finished
	if finished {
		return io.EOF
	}

	// Else continue
	return nil
}

func init() {
	// Prepare logger and load completed items
	log = logrus.New()
	readCompleted()

	// Prepare signal handler
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
}

func main() {
	// Get arguments
	flag.BoolVar(&continuous, "c", false, "continuously rewrite")
	flag.BoolVar(&shuffleBytes, "s", false, "shuffle bytes on rewrite")
	flag.IntVar(&threads, "t", 1, "threads")
	progname := filepath.Base(os.Args[0])
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `
Usage of %s:

  %s [flags] ./directory

Flags:
`, progname, progname)
		flag.PrintDefaults()
	}
	flag.Parse()

	// Check argument count
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}

	// Ensure quit
	go func() {
		<-done
		log.Infof("Finishing...")
		finished = true
	}()

	// Get all files and folders
	guard = make(chan struct{}, threads)
	for {
		err := filepath.Walk(flag.Arg(0), RewriteRouting)
		if err == io.EOF || finished {
			log.Infof("Program exited successfully")
			break
		} else if err != nil {
			close(guard)
			panic(err)
		}

		if !continuous {
			break
		}
	}

	// Cleanup
	wg.Wait()
}
