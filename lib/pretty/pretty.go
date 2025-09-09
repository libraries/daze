// Package pretty provides utilities for beautifying console output.
package pretty

import (
	"fmt"
	"log"
	"os"
	"slices"
	"strings"
)

// Progress represents a progress bar in the terminal.
type Progress struct {
	chardev bool
	current float64
}

// Update updates the progress bar to the specified percent (0 to 1).
func (p *Progress) Print(percent float64) {
	if percent > 1 {
		log.Panicln("pretty: the percent cannot be greater than 1")
	}
	if percent < p.current {
		log.Panicln("pretty: the percent cannot be decreased")
	}
	if percent != 0 && percent != 1 && percent-p.current < 0.01 {
		// Only update if the change is significant to avoid flickering.
		return
	}
	if percent == 1 && percent == p.current {
		// No need to update if already at 100%.
		return
	}
	if percent == 0 && p.chardev {
		// Save cursor position.
		log.Writer().Write([]byte{0x1b, 0x37})
	}
	if percent != 0 && p.chardev {
		// Load cursor position.
		log.Writer().Write([]byte{0x1b, 0x38})
	}
	p.current = percent
	cap := int(percent * 44)
	buf := []byte("[                                             ] 000%")
	for i := 1; i < cap+1; i++ {
		buf[i] = '='
	}
	buf[1+cap] = '>'
	num := fmt.Sprintf("%3d", int(percent*100))
	buf[48] = num[0]
	buf[49] = num[1]
	buf[50] = num[2]
	log.Println("pretty:", string(buf))
}

// NewProgress creates a new Progress instance.
func NewProgress() *Progress {
	s, err := os.Stdout.Stat()
	if err != nil {
		log.Panicln("pretty: cannot stat stdout:", err)
	}
	return &Progress{
		// Identify if we are displaying to a terminal or through a pipe or redirect.
		chardev: s.Mode()&os.ModeCharDevice == os.ModeCharDevice,
		current: 0,
	}
}

// ProgressWriter is an io.Writer that updates a progress bar as data is written.
type ProgressWriter struct {
	p *Progress
	m int64
	n int64
}

// Write writes data to the ProgressWriter and updates the progress bar.
func (p *ProgressWriter) Write(b []byte) (int, error) {
	l := len(b)
	p.m += int64(l)
	p.p.Print(float64(p.m) / float64(p.n))
	return l, nil
}

// NewProgressWriter creates a new ProgressWriter for a task of the given size.
//
// For example, to display progress while reading from a reader:
//
//	reader := io.TeeReader(io.LimitReader(os.Stdin, 1024), NewProgressWriter(1024))
//
// Or to display progress while writing to a writer:
//
//	writer := io.MultiWriter(os.Stdout, NewProgressWriter(1024))
func NewProgressWriter(n int64) *ProgressWriter {
	p := NewProgress()
	p.Print(0)
	return &ProgressWriter{
		p: p,
		m: 0,
		n: n,
	}
}

// Table represents a table structure with a head and body.
type Table struct {
	// Conf specifies the alignment for each column: "<" for left, ">" for right.
	// If conf has fewer entries than head, the remaining columns default to left alignment ("<").
	Conf []string
	// Head represents the head of the table.
	Head []string
	// Body represents the body of the table.
	Body [][]string
}

// Print prints the table to the console with proper alignment.
func (t *Table) Print() {
	conf := slices.Clone(t.Conf)
	for range len(t.Head) - len(conf) {
		conf = append(conf, "<")
	}
	size := make([]int, len(t.Head))
	for i, c := range t.Head {
		size[i] = len(c)
	}
	for _, r := range t.Body {
		for i, c := range r {
			size[i] = max(size[i], len(c))
		}
	}
	line := make([]string, len(t.Head))
	for i, c := range t.Head {
		l := size[i]
		switch conf[i] {
		case "<":
			line[i] = c + strings.Repeat(" ", l-len(c))
		case ">":
			line[i] = strings.Repeat(" ", l-len(c)) + c
		}
	}
	log.Println("pretty:", strings.Join(line, " "))
	for i, n := range size {
		line[i] = strings.Repeat("-", n)
	}
	log.Println("pretty:", strings.Join(line, "-"))
	for _, r := range t.Body {
		for i, c := range r {
			l := size[i]
			switch conf[i] {
			case "<":
				line[i] = c + strings.Repeat(" ", l-len(c))
			case ">":
				line[i] = strings.Repeat(" ", l-len(c)) + c
			}
		}
		log.Println("pretty:", strings.Join(line, " "))
	}
}

// NewTable creates a new Table instance.
func NewTable() *Table {
	return &Table{}
}

// Tree represents a node in a tree structure.
type Tree struct {
	Name string
	Leaf []*Tree
}

func (t *Tree) print(prefix string) {
	for i, elem := range t.Leaf {
		isLast := i == len(t.Leaf)-1
		branch := "├── "
		if isLast {
			branch = "└── "
		}
		log.Println("pretty:", prefix+branch+elem.Name)
		if len(elem.Leaf) > 0 {
			middle := "│   "
			if isLast {
				middle = "    "
			}
			elem.print(prefix + middle)
		}
	}
}

// Print prints the tree structure starting from the root node.
func (t *Tree) Print() {
	log.Println("pretty:", t.Name)
	t.print("")
}

// NewTree creates a new Tree node with the given name.
func NewTree(name string) *Tree {
	return &Tree{Name: name}
}
