package main

import (
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/golang/glog"
)

// Handles and processes the home page
func home(w http.ResponseWriter, r *http.Request) {
	tmpl.Execute(w, template.HTML(fmt.Sprintf(`http://%s/`, r.Host)))
}

// Upload a file, save and attribute a hash
func upload(w http.ResponseWriter, r *http.Request) {
	glog.Info("Upload request recieved")

	ip := clientIP(r)
	if isBlockedIP(ip) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "Forbidden.")
		return
	}
	if !allowRate(ip) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprintf(w, "Too Many Requests.")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+1024)
	if err := r.ParseMultipartForm(maxUploadBytes + 1024); err != nil {
		glog.Errorf("Error parsing form.")
		glog.Errorf("Error: %s", err.Error())

		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Bad request.")
		return
	}

	expiresRaw := r.FormValue("expires")
	expiresAtMs, err := parseExpires(expiresRaw)
	if err != nil {
		glog.Errorf("Error parsing expires.")
		glog.Errorf("Error: %s", err.Error())

		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Bad request. %s", err.Error())
		return
	}

	secret := hasFormKey(r, "secret")
	idLength := 12
	if secret {
		idLength = 32
	}

	var filename string
	var saveErr error

	file, header, err := r.FormFile("file")
	if err == nil {
		defer func() {
			_ = file.Close()
			glog.Infof(`File "%s" closed.`, header.Filename)
		}()
		filename = sanitizeFilename(header.Filename)
	} else if !errors.Is(err, http.ErrMissingFile) {
		glog.Errorf("Error retrieving file.")
		glog.Errorf("Error: %s", err.Error())

		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Bad request. Error retrieving file.")
		return
	} else {
		sourceURL := strings.TrimSpace(r.FormValue("url"))
		if sourceURL == "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "Bad request. Missing file or url.")
			return
		}
		filename = filenameFromURL(sourceURL)
		if filename == "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "Bad request. Invalid url.")
			return
		}
		saveErr = saveFromURL(sourceURL, filename, idLength, expiresAtMs, expiresRaw != "", w, r)
		if saveErr != nil {
			return
		}
		return
	}

	if filename == "" {
		filename = "file"
	}

	id, dirPath, err := allocateUploadDir(idLength, filename)
	if err != nil {
		glog.Errorf("Error creating upload directory.")
		glog.Errorf("Error: %s", err.Error())

		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "No storage available.")
		return
	}

	destPath := filepath.Join(dirPath, filename)
	dest, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0666)
	if err != nil {
		glog.Errorf("Error creating file.")
		glog.Errorf("Error: %s", err.Error())

		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Error creating file.")
		return
	}
	defer dest.Close()

	limit := io.LimitReader(file, maxUploadBytes+1)
	written, err := io.Copy(dest, limit)
	if err != nil {
		glog.Errorf("Error writing file.")
		glog.Errorf("Error: %s", err.Error())

		w.WriteHeader(http.StatusInsufficientStorage)
		fmt.Fprintf(w, "Insufficient Storage. Error storing file.")
		return
	}
	if written > maxUploadBytes {
		_ = os.RemoveAll(dirPath)
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		fmt.Fprintf(w, "File too large.")
		return
	}

	token := generateToken()
	meta := fileMeta{
		ExpiresAtMs:  expiresAtMs,
		Token:        token,
		OriginalName: filename,
		CreatedAtMs:  time.Now().UnixMilli(),
	}
	if err := metaStore.Save(id, meta); err != nil {
		glog.Errorf("Error writing metadata.")
		glog.Errorf("Error: %s", err.Error())

		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Error saving metadata.")
		return
	}

	respondUpload(w, r, id, token, expiresAtMs, expiresRaw != "")
}

func saveFromURL(sourceURL string, filename string, idLength int, expiresAtMs int64, expiresProvided bool, w http.ResponseWriter, r *http.Request) error {
	id, dirPath, err := allocateUploadDir(idLength, filename)
	if err != nil {
		glog.Errorf("Error creating upload directory.")
		glog.Errorf("Error: %s", err.Error())

		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "No storage available.")
		return err
	}

	destPath := filepath.Join(dirPath, filename)
	if err := downloadToFile(sourceURL, destPath); err != nil {
		_ = os.RemoveAll(dirPath)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Bad request. %s", err.Error())
		return err
	}

	token := generateToken()
	meta := fileMeta{
		ExpiresAtMs:  expiresAtMs,
		Token:        token,
		OriginalName: filename,
		CreatedAtMs:  time.Now().UnixMilli(),
	}
	if err := metaStore.Save(id, meta); err != nil {
		glog.Errorf("Error writing metadata.")
		glog.Errorf("Error: %s", err.Error())

		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Error saving metadata.")
		return err
	}

	respondUpload(w, r, id, token, expiresAtMs, expiresProvided)
	return nil
}

func respondUpload(w http.ResponseWriter, r *http.Request, id string, token string, expiresAtMs int64, expiresProvided bool) {
	w.Header().Set("X-Token", token)
	w.Header().Set("X-Expires", strconv.FormatInt(expiresAtMs, 10))
	fmt.Fprintf(w, "http://%s/%s\n", r.Host, id)
	if expiresProvided {
		fmt.Fprintf(w, "Expires: %s\n", time.UnixMilli(expiresAtMs).UTC().Format(time.RFC3339))
	}
}

func manage(w http.ResponseWriter, r *http.Request) {
	id, _ := parsePath(r.URL.Path)
	if id == "" {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "File Not Found.")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Bad request.")
		return
	}

	token := strings.TrimSpace(r.FormValue("token"))
	if token == "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Bad request. Missing token.")
		return
	}

	dirPath := filepath.Join(storageDir, id)
	meta, metaExists, err := metaStore.Get(id)
	if err != nil || !metaExists {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "File Not Found.")
		return
	}
	if token != meta.Token {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "Forbidden.")
		return
	}

	if hasFormKey(r, "delete") {
		_ = os.RemoveAll(dirPath)
		fmt.Fprintf(w, "OK\n")
		return
	}

	if hasFormKey(r, "expires") {
		expiresAtMs, err := parseExpires(r.FormValue("expires"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "Bad request. %s", err.Error())
			return
		}
		meta.ExpiresAtMs = expiresAtMs
		if err := metaStore.Save(id, meta); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "Error saving metadata.")
			return
		}
		w.Header().Set("X-Expires", strconv.FormatInt(expiresAtMs, 10))
		fmt.Fprintf(w, "OK\n")
		return
	}

	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintf(w, "Bad request.")
}

// Gets the file using the provided UUID on the URL
func getFile(w http.ResponseWriter, r *http.Request) {
	glog.Info("Retrieve request received")
	uuid, requestedName := parsePath(r.URL.Path)
	if uuid == "" {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "File Not Found.")
		return
	}
	var path string = filepath.Join(storageDir, uuid)

	glog.Infof(`Route "%s"`, r.URL.Path)
	glog.Infof(`Retrieving UUID "%s"`, uuid)
	glog.Infof(`Retrieving Path "%s"`, path)

	meta, metaExists, err := metaStore.Get(uuid)
	if err != nil {
		glog.Errorf(`Error reading metadata for "%s"`, path)
		glog.Errorf("Error: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Server Error.")
		return
	}

	if metaExists && meta.ExpiresAtMs > 0 && time.Now().UnixMilli() > meta.ExpiresAtMs {
		glog.Infof(`File "%s" expired`, uuid)
		_ = os.RemoveAll(path)
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "File Not Found.")
		return
	}

	files, err := os.ReadDir(path)
	if err != nil {
		glog.Errorf(`Error walking filepath "%s"`, path)
		glog.Errorf("Error: %s", err.Error())
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "File Not Found.")
		return
	}

	if len(files) <= 0 {
		glog.Errorf(`No files in directory "%s"`, path)
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "File Not Found.")
		return
	}

	var filename string
	for _, entry := range files {
		if entry.IsDir() {
			continue
		}
		if entry.Name() == metaFilename {
			continue
		}
		filename = entry.Name()
		break
	}
	if filename == "" {
		glog.Errorf(`No file payload found in "%s"`, path)
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "File Not Found.")
		return
	}
	glog.Infof(`Retrieving Filename "%s"`, fmt.Sprintf("./%s", filename))

	displayName := filename
	if requestedName != "" {
		displayName = sanitizeFilename(requestedName)
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", displayName))
	http.ServeFile(w, r, filepath.Join(path, filename))
}

func parsePath(urlPath string) (string, string) {
	trimmed := strings.Trim(urlPath, "/")
	if trimmed == "" {
		return "", ""
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) == 0 {
		return "", ""
	}
	if parts[0] == "s" && len(parts) > 1 {
		id := parts[1]
		name := ""
		if len(parts) > 2 {
			name = parts[2]
		}
		return id, name
	}
	id := parts[0]
	name := ""
	if len(parts) > 1 {
		name = parts[1]
	}
	return id, name
}

func allocateUploadDir(idLength int, filename string) (string, string, error) {
	ext := safeExt(filename)
	for {
		id := generateID(idLength) + ext
		dirPath := filepath.Join(storageDir, id)
		_, err := os.Stat(dirPath)
		if os.IsNotExist(err) {
			if err := os.MkdirAll(dirPath, 0777); err != nil {
				return "", "", err
			}
			return id, dirPath, nil
		}
	}
}

func filenameFromURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	name := filepath.Base(parsed.Path)
	if name == "." || name == "/" || name == "" {
		return "file"
	}
	return sanitizeFilename(name)
}

func downloadToFile(rawURL string, destPath string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return errors.New("invalid url")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("url must start with http:// or https://")
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	resp, err := client.Get(parsed.String())
	if err != nil {
		return errors.New("failed to fetch url")
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("url returned non-200 status")
	}
	if resp.ContentLength > 0 && resp.ContentLength > maxUploadBytes {
		return errors.New("file too large")
	}

	out, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0666)
	if err != nil {
		return errors.New("failed to create file")
	}
	defer out.Close()

	limit := io.LimitReader(resp.Body, maxUploadBytes+1)
	written, err := io.Copy(out, limit)
	if err != nil {
		return errors.New("failed to download file")
	}
	if written > maxUploadBytes {
		return errors.New("file too large")
	}

	return nil
}
