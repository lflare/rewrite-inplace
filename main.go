package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	progressbar "github.com/schollz/progressbar/v3"
)

const BLOCKSIZE = 128_000

func rewrite(path string, info os.FileInfo, err error) error {
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
		// Seek to position
		_, err = f.Seek(i, 0)
		if err != nil {
			return fmt.Errorf("Failed to seek to %d: %v", i, err)
		}

		// Read two bytes
		_, err = f.Read(buf1)
		if err != nil {
			return fmt.Errorf("Failed to read to buf1: %v", err)
		}

		// Swap bytes
		buf2[0], buf2[1] = buf1[1], buf1[0]

		// Write once
		_, err = f.WriteAt(buf2, i)
		if err != nil {
			return fmt.Errorf("Failed to write buf2: %v", err)
		}

		// Force filesystem sync
		err = f.Sync()
		if err != nil {
			return fmt.Errorf("Failed to sync: %v", err)
		}

		// Write original
		_, err = f.WriteAt(buf1, 1)
		if err != nil {
			return fmt.Errorf("Failed to write buf1: %v", err)
		}

		err = bar.Add(BLOCKSIZE)
		if err != nil {
			panic(err) // Really shouldn't ever reach this point
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
	err := filepath.Walk(os.Args[1], rewrite)
	if err != nil {
		log.Println(err)
	}
}
