//go:build !repomap

// repomap ships nothing in the default build — the symbol indexer is an
// opt-in dev tool. Rebuild with -tags repomap (see main.go).
package main

import "fmt"

func main() {
	fmt.Println("repomap: build with -tags repomap (optionally plus grammar_subset tags)")
}
