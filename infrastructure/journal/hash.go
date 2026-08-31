package journal

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
)

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
