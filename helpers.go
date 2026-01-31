package main

import (
	"encoding/json"
	"errors"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const metaFilename = "meta.json"
const hourMs = 60 * 60 * 1000
const defaultTokenLength = 24

type fileMeta struct {
	ExpiresAtMs  int64  `json:"expires_at_ms"`
	Token        string `json:"token"`
	OriginalName string `json:"original_name"`
	CreatedAtMs  int64  `json:"created_at_ms"`
}

// Generate a UUID
func GenerateUUID() string {
	return generateID(12)
}

func generateID(length int) string {
	var symbols = []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz1234567890")
	var id string
	for i := 0; i < length; i++ {
		id += string(symbols[rand.Intn(len(symbols)-1)])
	}

	return id
}

func parseExpires(raw string) (int64, error) {
	value := strings.TrimSpace(raw)
	nowMs := time.Now().UnixMilli()

	if value == "" {
		return clampExpires(nowMs+defaultExpirationHours*hourMs, nowMs), nil
	}

	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, errors.New("expires must be an integer")
	}

	if n >= 1_000_000_000_000 {
		if n <= nowMs {
			return 0, errors.New("expires must be in the future")
		}
		return clampExpires(n, nowMs), nil
	}

	if n <= 0 {
		return 0, errors.New("expires must be a positive number of hours")
	}

	expiresAtMs := nowMs + n*hourMs
	return clampExpires(expiresAtMs, nowMs), nil
}

func clampExpires(expiresAtMs int64, nowMs int64) int64 {
	minMs := nowMs + minExpirationHours*hourMs
	if expiresAtMs < minMs {
		expiresAtMs = minMs
	}
	maxMs := nowMs + maxExpirationHours*hourMs
	if expiresAtMs > maxMs {
		expiresAtMs = maxMs
	}
	return expiresAtMs
}

func writeMetaFile(dir string, meta fileMeta) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(dir+"/"+metaFilename, data, 0666)
}

func readMetaFile(dir string) (fileMeta, bool, error) {
	data, err := os.ReadFile(dir + "/" + metaFilename)
	if err != nil {
		if os.IsNotExist(err) {
			return fileMeta{}, false, nil
		}
		return fileMeta{}, false, err
	}
	var meta fileMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return fileMeta{}, false, err
	}
	return meta, true, nil
}

func hasFormKey(r *http.Request, key string) bool {
	if r.MultipartForm == nil {
		return false
	}
	if values, ok := r.MultipartForm.Value[key]; ok && len(values) > 0 {
		return true
	}
	if files, ok := r.MultipartForm.File[key]; ok && len(files) > 0 {
		return true
	}
	return false
}

func sanitizeFilename(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	if base == "." || base == "/" || base == "" {
		return "file"
	}
	return strings.ReplaceAll(base, "\"", "")
}

func safeExt(name string) string {
	ext := strings.TrimPrefix(filepath.Ext(name), ".")
	if ext == "" {
		return ""
	}
	if len(ext) > 10 {
		return ""
	}
	for _, ch := range ext {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			continue
		}
		return ""
	}
	return "." + ext
}

func generateToken() string {
	return generateID(defaultTokenLength)
}

func clientIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}
	host := r.RemoteAddr
	if strings.Contains(host, ":") {
		ip, _, err := net.SplitHostPort(host)
		if err == nil {
			return ip
		}
	}
	return host
}

func isBlockedIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, cidr := range blockedCIDRs {
		if cidr.Contains(parsed) {
			return true
		}
	}
	return false
}

type rateEntry struct {
	windowStart time.Time
	count       int
}

var (
	rateLimitMu sync.Mutex
	rateEntries = map[string]*rateEntry{}
)

func allowRate(ip string) bool {
	if rateLimitPerMin <= 0 {
		return true
	}
	now := time.Now()
	rateLimitMu.Lock()
	defer rateLimitMu.Unlock()

	entry, ok := rateEntries[ip]
	if !ok {
		rateEntries[ip] = &rateEntry{windowStart: now, count: 1}
		return true
	}
	if now.Sub(entry.windowStart) >= time.Minute {
		entry.windowStart = now
		entry.count = 1
		return true
	}
	if entry.count >= rateLimitPerMin {
		return false
	}
	entry.count++
	return true
}

func parseCIDRs(value string) []*net.IPNet {
	var out []*net.IPNet
	for _, raw := range strings.Split(value, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		_, cidr, err := net.ParseCIDR(raw)
		if err != nil {
			continue
		}
		out = append(out, cidr)
	}
	return out
}
