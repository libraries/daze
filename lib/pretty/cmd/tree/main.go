package main

import (
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/libraries/daze/lib/pretty"
)

func walk(path string) *pretty.Tree {
	info, err := os.Stat(path)
	if err != nil {
		log.Panicln("main:", err)
	}
	node := pretty.NewTree(info.Name())
	if info.IsDir() {
		l, err := os.ReadDir(path)
		if err != nil {
			log.Panicln("main:", err)
		}
		for _, e := range l {
			node.Leaf = append(node.Leaf, walk(filepath.Join(path, e.Name())))
		}
		// Sort the elements alphabetically for consistent output.
		slices.SortFunc(node.Leaf, func(a, b *pretty.Tree) int {
			return strings.Compare(a.Name, b.Name)
		})
	}
	return node
}

func main() {
	walk(".").Print()
}
