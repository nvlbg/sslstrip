package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
)

type server struct{}

var client = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func getCookieKey(cookie string) string {
	return strings.SplitN(cookie, "=", 2)[0]
}

func getHeaderWithCookies(oldHeaders http.Header, newHeaders http.Header) http.Header {
	if cookies, exists := newHeaders["Set-Cookie"]; exists {
		for _, value := range cookies {
			shouldOverwrite := false
			for i, cookie := range oldHeaders["Cookie"] {
				if getCookieKey(cookie) == getCookieKey(value) {
					oldHeaders["Cookie"][i] = value
					shouldOverwrite = true
					break
				}
			}

			if !shouldOverwrite {
				oldHeaders["Cookie"] = append(oldHeaders["Cookie"], value)
			}
		}
	}

	return oldHeaders
}

func makeRequest(req *http.Request, redirectCount int) (*http.Response, error) {
	if redirectCount >= 10 {
		return nil, errors.New("stopped after 10 redirects")
	}

	res, err := client.Do(req)

	if err != nil {
		return nil, err
	}

	var redirectMethod string
	var shouldRedirect, includeBody bool

	switch res.StatusCode {
	case 301, 302, 303:
		redirectMethod = req.Method
		shouldRedirect = true
		includeBody = false

		// RFC 2616 allowed automatic redirection only with GET and
		// HEAD requests. RFC 7231 lifts this restriction, but we still
		// restrict other methods to GET to maintain compatibility.
		// See Issue 18570.
		if req.Method != "GET" && req.Method != "HEAD" {
			redirectMethod = "GET"
		}
	case 307, 308:
		redirectMethod = req.Method
		shouldRedirect = true
		includeBody = true

		// Treat 307 and 308 specially, since they're new in
		// Go 1.8, and they also require re-sending the request body.
		if res.Header.Get("Location") == "" {
			// 308s have been observed in the wild being served
			// without Location headers. Since Go 1.7 and earlier
			// didn't follow these codes, just stop here instead
			// of returning an error.
			// See Issue 17773.
			shouldRedirect = false
			break
		}
		// if req.GetBody == nil && req.outgoingLength() != 0 {
		// 	// We had a request body, and 307/308 require
		// 	// re-sending it, but GetBody is not defined. So just
		// 	// return this response to the user instead of an
		// 	// error, like we did in Go 1.7 and earlier.
		// 	shouldRedirect = false
		// }
	}

	if shouldRedirect {
		loc := res.Header.Get("Location")
		if loc == "" {
			if res.Body != nil {
				res.Body.Close()
			}
			return nil, errors.New("wrong location redirect")
		}

		url, err := req.URL.Parse(loc)
		if err != nil {
			if res.Body != nil {
				res.Body.Close()
			}
			return nil, fmt.Errorf("failed to parse Location header %q: %v", loc, err)
		}

		proxyReq := &http.Request{
			Method: redirectMethod,
			URL:    url,
			Header: getHeaderWithCookies(req.Header, res.Header),
			Close:  req.Close,
		}

		if includeBody {
			proxyReq.Body = req.Body
		}

		return makeRequest(proxyReq, redirectCount+1)
	}

	return res, err
}

func (s server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	reqBody, err := ioutil.ReadAll(req.Body)
	if err != nil {
		fmt.Println("Could not get body")
		return
	}

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
	fmt.Printf("Body: %q\n", reqBody)
	fmt.Println("")

	proxyReq := &http.Request{
		Method: req.Method,
		URL:    req.URL,
		Header: req.Header,
		Host:   req.Host,
		Close:  req.Close,
		Body:   ioutil.NopCloser(bytes.NewReader(reqBody)),
	}

	res, err := makeRequest(proxyReq, 1)

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
