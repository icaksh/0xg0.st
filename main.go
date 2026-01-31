package main

import (
	"flag"
	"fmt"
	"html/template"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang/glog"
)

var port *uint64
var tmpl *template.Template
var host *string
var metaStore MetaStore
var sqlitePath string
var (
	maxUploadBytes         int64
	defaultExpirationMinutes int64
	minExpirationMinutes     int64
	maxExpirationMinutes     int64
	purgeInterval          time.Duration
	rateLimitPerMin        int
	blockedCIDRs           []*net.IPNet
)

const storageDir = "./storage"

// Dead simple router that just does the **perform** the job
func router(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		if r.Method == http.MethodPost {
			if strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
				upload(w, r)
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "Bad request.")
			return
		}
		home(w, r)
		return
	}

	if r.Method == http.MethodPost && strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
		manage(w, r)
		return
	}

	getFile(w, r)
}

// Route handling, logging and application serving
func main() {
	// Random seed creation
	rand.Seed(time.Now().Unix())

	// Home template initalization
	tmpl = template.Must(template.ParseFiles("./templates/index.html"))
	// Flags for the leveled logging

	host = flag.String("h", "0.0.0.0", "Address to serve on")
	port = flag.Uint64("p", 8000, "port")
	maxSize := flag.Int64("max_size", 512<<20, "max upload size in bytes")
	defaultExp := flag.Int64("default_expiration_minutes", 60*24*30, "default retention in minutes")
	minExp := flag.Int64("min_expiration_minutes", 60*24*30, "minimum retention in minutes")
	maxExp := flag.Int64("max_expiration_minutes", 60*24*365, "maximum retention in minutes")
	purge := flag.Duration("purge_interval", time.Minute, "expired file purge interval")
	rateLimit := flag.Int("rate_limit_per_min", 0, "upload rate limit per IP (0 disables)")
	blockCIDRs := flag.String("block_cidrs", "", "comma-separated CIDR ranges blocked from uploads")
	metaStoreOpt := flag.String("meta_store", "file", "metadata store: file or sqlite")
	sqliteOpt := flag.String("sqlite_path", "./storage/meta.db", "sqlite database path for metadata")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "USAGE: ./0xg0.st -p=8080 -stderrthreshold=[INFO|WARNING|FATAL] -log_dir=[string]\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	flag.Parse()
	glog.Flush()

	maxUploadBytes = *maxSize
	defaultExpirationMinutes = *defaultExp
	minExpirationMinutes = *minExp
	maxExpirationMinutes = *maxExp
	if defaultExpirationMinutes < minExpirationMinutes {
		defaultExpirationMinutes = minExpirationMinutes
	}
	if defaultExpirationMinutes > maxExpirationMinutes {
		defaultExpirationMinutes = maxExpirationMinutes
	}
	purgeInterval = *purge
	rateLimitPerMin = *rateLimit
	blockedCIDRs = parseCIDRs(*blockCIDRs)
	sqlitePath = *sqliteOpt

	switch strings.ToLower(strings.TrimSpace(*metaStoreOpt)) {
	case "sqlite":
		metaStore = &sqliteMetaStore{}
	default:
		metaStore = &fileMetaStore{}
	}
	if err := metaStore.Init(); err != nil {
		glog.Fatalf("metadata store init failed: %s", err.Error())
	}

	go purgeExpiredLoop()

	// Routing
	http.HandleFunc("/", router)
	http.ListenAndServe(fmt.Sprintf("%s:%d",*host,*port), nil)
}
