package main

import (
	"flag"
	"sslstrip"
)

var port = flag.Int("port", 8000, "port to listen on")
var filename = flag.String("filename", "", "the output log file or empty for stdout")
var postOnly = flag.Bool("post-only", false, "log only POST requests")
var logResponse = flag.Bool("log-response", false, "log responses")

func main() {
	flag.Parse()

	params := sslstrip.Params{
		Port:        *port,
		Filename:    *filename,
		PostOnly:    *postOnly,
		LogResponse: *logResponse,
	}

	sslstrip.Start(params)
}
