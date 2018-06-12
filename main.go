package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
)

type server struct{}

func (s server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	fmt.Println("Incoming request:")
	fmt.Printf("Method: %q\n", req.Method)
	fmt.Printf("URL: %q\n", req.URL)
	fmt.Printf("Proto, ProtoMajor, ProtoMinor: %q %q %q\n", req.Proto, req.ProtoMajor, req.ProtoMinor)
	fmt.Printf("Header: %q\n", req.Header)
	fmt.Printf("Host: %q\n", req.Host)
	fmt.Printf("Content-Length: %q\n", req.ContentLength)
	fmt.Printf("Transfer-encoding: %q\n", req.TransferEncoding)
	fmt.Printf("Close: %v\n", req.Close)
	fmt.Printf("Form: %q\n", req.Form)
	fmt.Printf("PostForm: %q\n", req.PostForm)
	fmt.Printf("MultipartForm: %q\n", req.MultipartForm)
	fmt.Printf("Trailer: %q\n", req.Trailer)
	fmt.Printf("RemoteAddr: %q\n", req.RemoteAddr)
	fmt.Printf("RequestUri: %q\n", req.RequestURI)
	fmt.Println("")

	// fmt.Fprintf(w, "Hello, world!")

	proxyReq := &http.Request{
		Method: req.Method,
		URL:    req.URL,
		Header: req.Header,
		Host:   req.Host,
	}

	res, err := http.DefaultClient.Do(proxyReq)

	if err != nil {
		fmt.Printf("Error: %q\n", err)
		return
	}

	fmt.Println("Fetched response:")
	fmt.Printf("Status: %q\n", res.Status)
	fmt.Printf("StatusCode: %q\n", res.StatusCode)
	fmt.Printf("Proto: %q\n", res.Proto)
	fmt.Printf("ProtoMajor: %q\n", res.ProtoMajor)
	fmt.Printf("ProtoMinor: %q\n", res.ProtoMinor)
	fmt.Printf("Header: %q\n", res.Header)
	fmt.Printf("Content-Length: %q\n", res.ContentLength)
	fmt.Printf("Transfer-Encoding: %q\n", res.TransferEncoding)
	fmt.Printf("Uncompressed: %v\n", res.Uncompressed)
	fmt.Printf("Trailer: %v\n", res.Trailer)
	fmt.Println("")

	body, err := ioutil.ReadAll(res.Body)

	if err != nil {
		fmt.Printf("Error: %q\n", err)
		return
	}

	header := w.Header()
	for key, value := range res.Header {
		header[key] = value
	}

	w.WriteHeader(res.StatusCode)
	w.Write(body)
}

func main() {
	var s server
	log.Fatal(http.ListenAndServe("0.0.0.0:8000", s))
}
