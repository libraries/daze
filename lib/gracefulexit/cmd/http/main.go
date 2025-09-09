package main

import (
	"log"
	"net"
	"net/http"

	"github.com/libraries/daze/lib/gracefulexit"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World!"))
		w.Write([]byte("\n"))
	})
	log.Println("main: listen and server on 127.0.0.1:8080")
	l, err := net.Listen("tcp", "127.0.0.1:8080")
	if err != nil {
		log.Panicln("main:", err)
	}
	server := http.Server{}
	go server.Serve(l)
	gracefulexit.Wait()
	log.Println("main: server close")
	server.Close()
	log.Println("main: done")
}
