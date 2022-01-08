package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
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

var log *logrus.Logger

const BLOCKSIZE = 128_000

var completed = []string{}

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

func writeSync(file *os.File, bytes []byte, offset int64) error {
	// Write once
	_, err := file.WriteAt(bytes, offset)
	if err != nil {
		return fmt.Errorf("failed to write buf2: %v", err)
	}
	return nil
}

func Rewrite(path string, info os.FileInfo, err error) error {
	// Return early if already completed
	for _, b := range completed {
		if b == path {
			log.Infof("Skipping file '%s'\n", path)
			return nil
		}
	}

	// Get file info if empty
	if info == nil {
		info, err = os.Stat(path)
		if err != nil {
			return err
		}
	}

	// Return early if error
	if err != nil {
		return err
	}

	// Return early if not file
	if info.IsDir() {
		return nil
	}

	// Return early if too small to actually balance
	if info.Size() < BLOCKSIZE {
		return nil
	}

	// Prepare signal catching
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	// Open file
	file, err := os.OpenFile(path, os.O_RDWR, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer file.Close()

	// Open backup file
	backupPath := fmt.Sprintf("%s.bak", path)
	backupFile, err := os.Create(backupPath)
	if err != nil {
		return err
	}
	defer backupFile.Close()

	// Copy to backup file
	bar := progressbar.DefaultBytes(info.Size(), "backing up")
	written, err := io.Copy(io.MultiWriter(backupFile, bar), file)
	if err != nil {
		return err
	}
	bar.Finish()
	log.Infof("Backed up file '%s' of size %d\n", path, written)

	// Calculate original hash
	oldHash := sha256.New()
	if _, err := io.Copy(oldHash, file); err != nil {
		return err
	}

	// Prepare buffer
	buf := make([]byte, 2)

	// Print
	log.Infof("Rewriting file '%s'\n", path)

	// Run two times, swapping both times
	for n := 0; n < 2; n++ {
		// Prepare progress bar
		bar := progressbar.DefaultBytes(info.Size(), fmt.Sprintf("rewriting [%d/2]", n+1))

		// Loop through whole file in steps of block size
		for i := int64(0); i < info.Size()-2; i += BLOCKSIZE {
			// Read two bytes at offset
			_, err = file.ReadAt(buf, i)
			if err != nil {
				return fmt.Errorf("failed to read to buf: %v", err)
			}

			// Swap bytes
			buf[0], buf[1] = buf[1], buf[0]

			// Write swapped bytes
			err = writeSync(file, buf, i)
			if err != nil {
				return err
			}

			// Propagate progress bar
			err = bar.Add(BLOCKSIZE)
			if err != nil {
				err = bar.Finish()
				if err != nil {
					panic(err) // Really shouldn't ever reach this point
				}
			}
		}

		// Force filesystem sync
		err = file.Sync()
		if err != nil {
			return fmt.Errorf("failed to sync: %v", err)
		}

		// Finish progress bar
		err = bar.Finish()
		if err != nil {
			panic(err) // Really shouldn't ever reach this point
		}
	}

	// Calculate new hash
	newHash := sha256.New()
	if _, err := io.Copy(newHash, file); err != nil {
		return err
	}

	// If for some reason, hashes are not the same, restore backup
	oldHashString := fmt.Sprintf("%x", oldHash.Sum(nil))
	newHashString := fmt.Sprintf("%x", newHash.Sum(nil))
	if oldHashString != newHashString {
		io.Copy(file, backupFile)
		os.Remove(backupPath)
		return fmt.Errorf("unexpected hash of file '%s', '%s' != '%s', restoring backup", path, oldHashString, newHashString)
	} else {
		os.Remove(backupPath)
	}

	// Save progress
	completed = append(completed, path)
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
	log = logrus.New()
	readCompleted()
}

func main() {
	// Get all files and folders
	err := filepath.Walk(os.Args[1], Rewrite)
	if err == io.EOF {
		log.Infof("program exited successfully")
	} else if err != nil {
		panic(err)
	}
}
