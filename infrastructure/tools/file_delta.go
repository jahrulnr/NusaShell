package tools

import (
	"bytes"
	"os"
	"unicode/utf8"
)

type pathText struct {
	text   string
	exists bool
	dir    bool
	exact  bool
}

func isTrackableText(data []byte) bool {
	return utf8.Valid(data) && bytes.IndexByte(data, 0) < 0
}

func readPathText(path string) pathText {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return pathText{exact: true}
		}
		return pathText{}
	}
	if info.IsDir() {
		return pathText{exists: true, dir: true}
	}
	if !info.Mode().IsRegular() {
		return pathText{exists: true}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return pathText{exists: true}
	}
	if !isTrackableText(data) {
		return pathText{exists: true}
	}
	return pathText{text: string(data), exists: true, exact: true}
}
