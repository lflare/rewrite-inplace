package main

import (
	"fmt"
	"io"
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

func writeSync(file *os.File, bytes []byte, offset int64) error {
	// Write once
	_, err := file.WriteAt(bytes, offset)
	if err != nil {
		return fmt.Errorf("failed to write buf2: %v", err)
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
			_, err = f.ReadAt(buf, i)
			if err != nil {
				return fmt.Errorf("failed to read to buf: %v", err)
			}

			// Swap bytes
			buf[0], buf[1] = buf[1], buf[0]

			// Write swapped bytes
			err = writeSync(f, buf, i)
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
		err = f.Sync()
		if err != nil {
			return fmt.Errorf("failed to sync: %v", err)
		}

		// Finish progress bar
		err = bar.Finish()
		if err != nil {
			panic(err) // Really shouldn't ever reach this point
		}
	}

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
