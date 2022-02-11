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
	"syscall"
	"time"

	progressbar "github.com/schollz/progressbar/v3"
	"github.com/sirupsen/logrus"
)

// Constants
const BLOCKSIZE = 128_000

// Configurations
var shuffleBytes bool
var continuous bool

// Runtime globals
var log *logrus.Logger
var done = make(chan os.Signal, 1)

// Completed files
type Completed struct {
	completedFiles  []string `json:"completed_files"`
	completedInodes []uint64 `json:"completed_inodes`
}

var completed Completed

func saveCompleted() {
	file, _ := json.MarshalIndent(completed, "", " ")
	ioutil.WriteFile("progress.json", file, 0644)
}

func readCompleted() {
	bytes, err := ioutil.ReadFile("progress.json")
	if err == nil {
		json.Unmarshal(bytes, &completed)
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
	defer bar.Finish()

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
	defer bar.Finish()

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
		restoreBackupFile(path, info)
		return fmt.Errorf("rewrite failed, hash mismatch '%s' != '%s'", oldHashString, newHashString)
	}

	// Delete backup file
	deleteBackupFile(path)

	// Return no error
	return nil
}

func Rewrite(path string, info os.FileInfo, err error) error {
	// Get inode
	stat, _ := info.Sys().(*syscall.Stat_t)
	inode := stat.Ino

	// Return early if already completed and not continuously rewriting
	if !continuous {
		for _, b := range completed.completedFiles {
			if b == path {
				log.Infof("Skipping file '%s'\n", path)
				return nil
			}
		}

		for _, b := range completed.completedInodes {
			if b == inode {
				log.Infof("Skipping inode '%s'\n", inode)
				return nil
			}
		}
	}

	// Return early if error
	if err != nil {
		return err
	}

	// Get file info if empty
	if info == nil {
		info, err = os.Stat(path)
		if err != nil {
			return err
		}
	}

	// Return early if not file
	if info.IsDir() {
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
	completed.completedFiles = append(completed.completedFiles, path)
	if stat != nil {
		completed.completedInodes = append(completed.completedInodes, stat.Ino)
	}
	saveCompleted()

	// Check if signal was raised
	select {
	case <-done:
		return io.EOF
	case <-time.After(1):
	}

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

	// Get all files and folders
	for {
		err := filepath.Walk(flag.Arg(0), Rewrite)
		if err == io.EOF {
			log.Infof("program exited successfully")
			break
		} else if err != nil {
			panic(err)
		}

        if !continuous {
            break
        }
	}
}
