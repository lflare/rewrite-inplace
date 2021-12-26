package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
)

const (
	letterBytes   = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	letterIdxBits = 6                    // 6 bits to represent a letter index
	letterIdxMask = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
	letterIdxMax  = 63 / letterIdxBits   // # of letter indices fitting in 63 bits
)

var src = rand.NewSource(time.Now().UnixNano())

func RandStringBytesMaskImprSrcUnsafe(n int) string {
	b := make([]byte, n)
	// A src.Int63() generates 63 random bits, enough for letterIdxMax characters!
	for i, cache, remain := n-1, src.Int63(), letterIdxMax; i >= 0; {
		if remain == 0 {
			cache, remain = src.Int63(), letterIdxMax
		}
		if idx := int(cache & letterIdxMask); idx < len(letterBytes) {
			b[i] = letterBytes[idx]
			i--
		}
		cache >>= letterIdxBits
		remain--
	}

	return *(*string)(unsafe.Pointer(&b))
}

func ByteCountIEC(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB",
		float64(b)/float64(div), "KMGTPE"[exp])
}

func VerifyFile(path string, data string) bool {
	d1, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}

	return data == string(d1)
}

func TestRewrite(t *testing.T) {
	// Get temporary directory
	dir := t.TempDir()

	// Prepare sizes
	sizes := []int{
		1,
		2,
		3,
		512,
		1024,
		2048,
		4096,
		8192,
		BLOCKSIZE - 3,
		BLOCKSIZE - 2,
		BLOCKSIZE - 1,
		BLOCKSIZE,
		BLOCKSIZE + 1,
		BLOCKSIZE + 2,
		BLOCKSIZE + 3,
		int(math.Pow(2, 24)),
	}

	// Loop through sizes
	for i, size := range sizes {
		// Prepare path
		path := fmt.Sprintf("%s/%d", dir, i)

		// Generate random sequence of bytes 16 megabytes
		randomString := RandStringBytesMaskImprSrcUnsafe(size)
		randomBytes := []byte(randomString)

		// Log attempt
		t.Logf("starting test with %d - %s", size, ByteCountIEC(int64(size)))

		// Create file
		f, err := os.Create(path)
		if err != nil {
			panic(err)
		}
		defer f.Close()

		// Write bytes in file
		_, err = f.Write(randomBytes)
		if err != nil {
			panic(err)
		}

		// Verify file
		writtenBytes, err := os.ReadFile(path)
		if err != nil {
			panic(err)
		}

		// Ensure equal
		assert.Equal(t, randomBytes, writtenBytes, "[step 1] written bytes != random bytes")

		// Rewrite file
		Rewrite(path, nil, err)

		// Ensure equal
		assert.Equal(t, randomBytes, writtenBytes, "[step 2] rewritten bytes != written bytes")

		// Log success
		t.Logf("successfully tested with %d - %s", size, ByteCountIEC(int64(size)))
	}
}
