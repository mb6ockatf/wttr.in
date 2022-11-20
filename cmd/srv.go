package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	lru "github.com/hashicorp/golang-lru"
)

const uplinkSrvAddr = "127.0.0.1:9002"
const uplinkTimeout = 30
const prefetchInterval = 300
const lruCacheSize = 12800

// plainTextAgents contains signatures of the plain-text agents
var plainTextAgents = []string{
	"curl",
	"httpie",
	"lwp-request",
	"wget",
	"python-httpx",
	"python-requests",
	"openbsd ftp",
	"powershell",
	"fetch",
	"aiohttp",
	"http_get",
	"xh",
}

var lruCache *lru.Cache

type responseWithHeader struct {
	InProgress bool      // true if the request is being processed
	Expires    time.Time // expiration time of the cache entry

	Body       []byte
	Header     http.Header
	StatusCode int // e.g. 200
}

func init() {
	var err error
	lruCache, err = lru.New(lruCacheSize)
	if err != nil {
		panic(err)
	}

	dialer := &net.Dialer{
		Timeout:   uplinkTimeout * time.Second,
		KeepAlive: uplinkTimeout * time.Second,
		DualStack: true,
	}

	http.DefaultTransport.(*http.Transport).DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, uplinkSrvAddr)
	}

	initPeakHandling()
}

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func serveHTTP(mux *http.ServeMux, port int, errs chan<- error) {
	errs <- http.ListenAndServe(fmt.Sprintf(":%d", port), mux)
}

func serveHTTPS(mux *http.ServeMux, port int, errs chan<- error) {
	errs <- http.ListenAndServeTLS(fmt.Sprintf(":%d", port),
		Conf.Server.TLSCertFile, Conf.Server.TLSKeyFile, mux)
}

func main() {
	var (
		// mux is main HTTP/HTTP requests multiplexer.
		mux *http.ServeMux = http.NewServeMux()

		// logger is optimized requests logger.
		logger *RequestLogger = NewRequestLogger(
			Conf.Logging.AccessLog,
			time.Duration(Conf.Logging.Interval)*time.Second)

		// errs is the servers errors channel.
		errs chan error = make(chan error, 1)

		// numberOfServers started. If 0, exit.
		numberOfServers int
	)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if err := logger.Log(r); err != nil {
			log.Println(err)
		}
		// printStat()
		response := processRequest(r)

		copyHeader(w.Header(), response.Header)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(response.StatusCode)
		w.Write(response.Body)
	})

	if Conf.Server.PortHTTP != 0 {
		go serveHTTP(mux, Conf.Server.PortHTTP, errs)
		numberOfServers++
	}
	if Conf.Server.PortHTTPS != 0 {
		go serveHTTPS(mux, Conf.Server.PortHTTPS, errs)
		numberOfServers++
	}
	if numberOfServers == 0 {
		log.Println("no servers configured; exiting")
		return
	}
	log.Fatal(<-errs) // block until one of the servers writes an error
}
