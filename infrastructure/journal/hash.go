package journal

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
)

const maxDiffBytes = 1 << 20 // 1 MiB

func hashContent(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hashFile(path string) (hash string, size int64, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	if info.IsDir() {
		return "", info.Size(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	return hashContent(data), int64(len(data)), nil
}

func isTextDiffEligible(data []byte, maxSize int64) bool {
	if int64(len(data)) > maxSize {
		return false
	}
	for _, b := range data {
		if b == 0 {
			return false
		}
	}
	return true
}

func readFileLimited(path string, maxSize int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxSize {
		return nil, errOversized
	}
	return os.ReadFile(path)
}

var errOversized = os.ErrInvalid
