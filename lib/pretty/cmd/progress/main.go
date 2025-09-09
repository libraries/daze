package main

import (
	"time"

	"github.com/libraries/daze/lib/pretty"
)

func main() {
	progress := pretty.NewProgress()
	progress.Print(0)
	for i := range 1024 {
		time.Sleep(time.Millisecond * 4)
		progress.Print(float64(i+1) / 1024)
	}
	progress.Print(1)
}
