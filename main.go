package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

type server struct{}

type clientLink struct {
	clientIP string
	url      string
}

var client = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

var ignoredHeaders = map[string]struct{}{
	// content length will change after stripping and needs to be recalculated
	"Content-Length": struct{}{},
	// hsts and hpkp headers
	"Public-Key-Pins":             struct{}{},
	"Public-Key-Pins-Report-Only": struct{}{},
	"Strict-Transport-Security":   struct{}{},
}

var storedLinks map[clientLink]string = make(map[clientLink]string, 0)
var mu sync.RWMutex

func getLink(cl clientLink) (string, bool) {
	mu.RLock()
	defer mu.RUnlock()
	link, exists := storedLinks[cl]

	// for key, val := range storedLinks {
	// 	fmt.Printf("RemoteAddr: %q\t%q\t%q\n", key.clientIP, key.url, val)
	// }
	// fmt.Println()

	return link, exists
}

func setLink(cl clientLink, link string) {
	mu.Lock()
	defer mu.Unlock()
	storedLinks[cl] = link
}

func normalizeIP(remoteAddr string) string {
	return strings.Split(remoteAddr, ":")[0]
}

func normalizeUrl(link string) (string, error) {
	originalUrl, err := url.Parse(link)

	if err != nil {
		return "", err
	}

	if originalUrl.Path == "" {
		originalUrl.Path = "/"
	}

	return originalUrl.String(), nil
}

func makeRequest(req *http.Request) (*http.Response, error) {
	// error will never happen
	u, _ := normalizeUrl(req.URL.String())

	cl := clientLink{
		clientIP: normalizeIP(req.RemoteAddr),
		url:      u,
	}

	// restore original link if cached
	if link, exists := getLink(cl); exists {
		originalUrl, err := req.URL.Parse(link)

		if err != nil {
			return nil, err
		}

		req.URL = originalUrl
	}

	// make request to server
	res, err := client.Do(req)

	if err != nil {
		return nil, err
	}

	return res, nil
}

func stripResponse(req *http.Request, res *http.Response) ([]byte, error) {
	// strip location header if exists
	location := res.Header.Get("Location")
	if strings.HasPrefix(location, "https") {
		strippedLocation, err := normalizeUrl("http" + location[5:])

		if err != nil {
			return nil, err
		}

		// cache original location
		forgedKey := clientLink{
			clientIP: normalizeIP(req.RemoteAddr),
			url:      strippedLocation,
		}
		setLink(forgedKey, location)
		res.Header.Set("Location", strippedLocation)
	}

	// strip secure cookies
	if cookies, exists := res.Header["Set-Cookie"]; exists {
		for i := 0; i < len(cookies); i++ {
			cookie := cookies[i]
			if idx := strings.LastIndex(cookie, "Secure"); idx != -1 {
				cookies[i] = cookie[:idx] + cookie[idx+6:]
			}
		}
	}

	defer res.Body.Close()
	body, err := ioutil.ReadAll(res.Body)

	if err != nil {
		return nil, fmt.Errorf("Error when reading response body: %q\n", err)
	}

	// strip all https links in the response body
	regex, _ := regexp.Compile("(https://[a-zA-Z0-9_:#@%/;$()~_?+-=\\.&]*)")
	strippedBody := regex.ReplaceAllFunc(body, func(u []byte) []byte {
		url := string(u)
		strippedUrl, err := normalizeUrl("http://" + url[8:])

		if err != nil {
			fmt.Printf("Warning: could not normalize url %s: %q\n", url, err)
			return u
		}

		// store original link for future requests
		key := clientLink{
			clientIP: normalizeIP(req.RemoteAddr),
			url:      strippedUrl,
		}
		setLink(key, url)
		return []byte(strippedUrl)
	})

	return strippedBody, nil
}

func (s server) ServeHTTP(responseWriter http.ResponseWriter, req *http.Request) {
	// read request body
	reqBody, err := ioutil.ReadAll(req.Body)
	if err != nil {
		fmt.Println("Could not get body")
		return
	}

	fmt.Println("Incoming request:")
	// fmt.Printf("Method: %q\n", req.Method)
	// fmt.Printf("URL: %q\n", req.URL)
	// fmt.Printf("Proto, ProtoMajor, ProtoMinor: %q %q %q\n", req.Proto, req.ProtoMajor, req.ProtoMinor)
	// fmt.Printf("Header: %q\n", req.Header)
	// fmt.Printf("Host: %q\n", req.Host)
	// fmt.Printf("Content-Length: %q\n", req.ContentLength)
	// fmt.Printf("Transfer-encoding: %q\n", req.TransferEncoding)
	// fmt.Printf("Close: %v\n", req.Close)
	// fmt.Printf("Form: %q\n", req.Form)
	// fmt.Printf("PostForm: %q\n", req.PostForm)
	// fmt.Printf("MultipartForm: %q\n", req.MultipartForm)
	// fmt.Printf("Trailer: %q\n", req.Trailer)
	// fmt.Printf("RemoteAddr: %q\n", req.RemoteAddr)
	// fmt.Printf("RequestUri: %q\n", req.RequestURI)
	// fmt.Printf("Body: %q\n", reqBody)
	// fmt.Println("")

	// build request to be made
	proxyReq := &http.Request{
		Method:     req.Method,
		URL:        req.URL,
		Header:     req.Header,
		Host:       req.Host,
		Close:      req.Close,
		Body:       ioutil.NopCloser(bytes.NewReader(reqBody)),
		RemoteAddr: req.RemoteAddr,
	}

	// make request to the server
	res, err := makeRequest(proxyReq)

	if err != nil {
		fmt.Printf("Error occurred when making proxy request: %q\n", err)
		return
	}

	// decompress body before stripping if compressed
	if res.Header.Get("Content-Encoding") == "gzip" {
		reader, err := gzip.NewReader(res.Body)
		if err != nil {
			fmt.Printf("Error when decompressing response body: %q\n", err)
			return
		}
		res.Body = reader
	}

	// strip response
	strippedBody, err := stripResponse(req, res)
	if err != nil {
		fmt.Printf("Error when stripping response: %q\n", err)
		return
	}

	// compress stripped body if necessary
	if res.Header.Get("Content-Encoding") == "gzip" {
		var b bytes.Buffer
		writer := gzip.NewWriter(&b)
		writer.Write(strippedBody)
		writer.Flush()
		writer.Close()
		strippedBody = b.Bytes()
	}

	// fmt.Println("Fetched response:")
	// fmt.Printf("Status: %q\n", res.Status)
	// fmt.Printf("StatusCode: %q\n", res.StatusCode)
	// fmt.Printf("Proto: %q\n", res.Proto)
	// fmt.Printf("ProtoMajor: %q\n", res.ProtoMajor)
	// fmt.Printf("ProtoMinor: %q\n", res.ProtoMinor)
	// fmt.Printf("Header: %q\n", res.Header)
	// fmt.Printf("Content-Length: %q\n", res.ContentLength)
	// fmt.Printf("Transfer-Encoding: %q\n", res.TransferEncoding)
	// fmt.Printf("Uncompressed: %v\n", res.Uncompressed)
	// fmt.Printf("Trailer: %v\n", res.Trailer)
	// fmt.Println("")

	// build headers that will be returned to the client
	header := responseWriter.Header()
	for key, value := range res.Header {
		if _, ignored := ignoredHeaders[key]; !ignored {
			header[key] = value
		}
	}

	responseWriter.WriteHeader(res.StatusCode)

	// send stripped body to client
	_, err = responseWriter.Write(strippedBody)

	if err != nil {
		fmt.Printf("Error when sending response body to client: %q\n", err)
		return
	}
}

func main() {
	var s server
	log.Fatal(http.ListenAndServe("0.0.0.0:8000", s))
}
