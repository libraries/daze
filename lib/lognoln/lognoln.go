package lognoln

import (
	"bytes"
	"log"
)

// Print print to the standard logger with a newline removed from the end of the output.
func Print(v ...any) {
	w := new(bytes.Buffer)
	log.New(w, "", log.Flags()).Println(v...)
	log.Default().Writer().Write(w.Bytes()[:len(w.Bytes())-1])
}
