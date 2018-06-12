package main

import (
	"fmt"
	"log"
	"net/http"
)

type server struct{}

func (s server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	fmt.Printf("%q\n", req.Host)
	fmt.Fprintf(w, "Hello, world!")
}

func main() {
	var s server
	log.Fatal(http.ListenAndServe("0.0.0.0:8000", s))
}
