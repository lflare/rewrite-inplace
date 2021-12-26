package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	progressbar "github.com/schollz/progressbar/v3"
)

const BLOCKSIZE = 128_000

func writeSync(file *os.File, bytes []byte, offset int64) error {
	// Write once
	_, err := file.WriteAt(bytes, offset)
	if err != nil {
		return fmt.Errorf("failed to write buf2: %v", err)
	}

	// Force filesystem sync
	err = file.Sync()
	if err != nil {
		return fmt.Errorf("failed to sync: %v", err)
	}

	return nil
}

func Rewrite(path string, info os.FileInfo, err error) error {
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
	f, err := os.OpenFile(path, os.O_RDWR, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer f.Close()

	// Prepare buffer
	buf1 := make([]byte, 2)
	buf2 := make([]byte, 2)

	// Print
	fmt.Printf("Balancing file '%s'\n", path)

	// Prepare progress bar
	bar := progressbar.Default(info.Size() - 2)

	// Loop through whole file in steps of block size
	for i := int64(0); i < info.Size()-2; i += BLOCKSIZE {
		// Read two bytes at offset
		_, err = f.ReadAt(buf1, i)
		if err != nil {
			return fmt.Errorf("failed to read to buf1: %v", err)
		}

		// Swap bytes
		buf2[0], buf2[1] = buf1[1], buf1[0]

		// Write swapped bytes
		err = writeSync(f, buf2, i)
		if err != nil {
			return err
		}

		// Write original bytes
		err = writeSync(f, buf1, i)
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

		// Check if signal was raised
		select {
		case <-done:
			return nil
		case <-time.After(1):
		}
	}

	// Finish progress bar
	err = bar.Finish()
	if err != nil {
		panic(err) // Really shouldn't ever reach this point
	}

	return nil
}

func main() {
	// Get all files and folders
	err := filepath.Walk(os.Args[1], Rewrite)
	if err != nil {
		log.Println(err)
	}
}
