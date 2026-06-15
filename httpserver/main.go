package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// decodeJSON decodes a JSON request body into v.
func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// ── Configuration ────────────────────────────────────────────────────────────

var (
	uploadDir       string
	port            string
	serverEnabled   bool
	allowedOrigins  []string
	serverBooksDirs []string
)

func init() {
	abs, err := filepath.Abs("./uploads")
	if err != nil {
		log.Fatalf("Cannot resolve uploads path: %v", err)
	}
	uploadDir = abs

	// Server books directories (for reading books placed on the server)
	// Supports comma-separated paths, e.g. "/app/books1,/app/books2"
	rawBooksDir := getEnv("SERVER_BOOKS_DIR", "")
	if rawBooksDir != "" {
		for _, d := range strings.Split(rawBooksDir, ",") {
			d = strings.TrimSpace(d)
			if d == "" {
				continue
			}
			abs, err = filepath.Abs(d)
			if err != nil {
				log.Printf("Warning: Cannot resolve server books path %q: %v", d, err)
				continue
			}
			serverBooksDirs = append(serverBooksDirs, abs)
		}
	}

	port = getEnv("PORT", "8080")
	serverEnabled = os.Getenv("ENABLE_HTTP_SERVER") == "true"

	// Allowed origins
	raw := os.Getenv("ALLOWED_ORIGINS")
	for _, o := range strings.Split(raw, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			allowedOrigins = append(allowedOrigins, o)
		}
	}
	if len(allowedOrigins) == 0 {
		log.Println("Warning: No ALLOWED_ORIGINS configured. All cross-origin requests will be allowed. " +
			"Set ALLOWED_ORIGINS to a comma-separated list of trusted origins to restrict access.")
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// applyCorsHeaders mirrors the JS implementation. Returns true when CORS is allowed.
func applyCorsHeaders(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	w.Header().Set("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	if origin == "" {
		return false
	}
	allowed := len(allowedOrigins) == 0 // empty list ⟹ allow all origins
	if !allowed {
		for _, o := range allowedOrigins {
			if o == origin {
				allowed = true
				break
			}
		}
	}
	if allowed {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		return true
	}
	return false
}

func getServerOrigin(r *http.Request) string {
	host := r.Host
	if host == "" {
		return ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	} else if fwd := r.Header.Get("X-Forwarded-Proto"); fwd != "" {
		scheme = strings.SplitN(fwd, ",", 2)[0]
		scheme = strings.TrimSpace(scheme)
	}
	return scheme + "://" + host
}

// sanitizeFilename keeps only the base name and replaces Windows-illegal chars.
func sanitizeFilename(name string) string {
	base := filepath.Base(name)
	// Replace characters illegal on Windows filesystems
	illegal := `\/:*?"<>|`
	for _, c := range illegal {
		base = strings.ReplaceAll(base, string(c), "_")
	}
	return base
}

// resolveSafePath resolves a path under uploadDir and rejects traversal attempts.
func resolveSafePath(segments ...string) (string, error) {
	args := append([]string{uploadDir}, segments...)
	target := filepath.Join(args...)
	rel, err := filepath.Rel(uploadDir, target)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("Invalid path")
	}
	return target, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writePlain(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(msg))
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func handleUpload(w http.ResponseWriter, r *http.Request, dirParam string) {
	ct := r.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(ct)
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		writePlain(w, http.StatusBadRequest, "Invalid Content-Type. Expected multipart/form-data")
		return
	}
	boundary := params["boundary"]
	if boundary == "" {
		writePlain(w, http.StatusBadRequest, "Missing boundary in Content-Type")
		return
	}

	mr := multipart.NewReader(r.Body, boundary)
	var fileData []byte
	var filename string

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writePlain(w, http.StatusBadRequest, "Error reading multipart data")
			return
		}
		fn := part.FileName()
		if fn != "" {
			filename = fn
			fileData, err = io.ReadAll(part)
			if err != nil {
				writePlain(w, http.StatusInternalServerError, "Internal Server Error")
				return
			}
			log.Printf("Found file: %s, size: %d bytes", filename, len(fileData))
		}
		part.Close()
	}

	if fileData == nil || filename == "" {
		writePlain(w, http.StatusBadRequest, "No valid file uploaded")
		return
	}

	safeFilename := sanitizeFilename(filename)
	if safeFilename == "" || safeFilename == "." || safeFilename == ".." {
		writePlain(w, http.StatusBadRequest, "Invalid filename")
		return
	}

	targetDir, err := resolveSafePath(dirParam)
	if err != nil {
		writePlain(w, http.StatusBadRequest, err.Error())
		return
	}
	filePath, err := resolveSafePath(dirParam, safeFilename)
	if err != nil {
		writePlain(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		log.Printf("MkdirAll error: %v", err)
		writePlain(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	if err := os.WriteFile(filePath, fileData, 0o644); err != nil {
		log.Printf("File write error: %v", err)
		writePlain(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"filename":  safeFilename,
		"directory": dirParam,
		"message":   "File uploaded successfully",
	})
}

func handleDownload(w http.ResponseWriter, r *http.Request, dirParam string) {
	filename := r.URL.Query().Get("filename")
	if filename == "" {
		writePlain(w, http.StatusBadRequest, "Missing filename parameter")
		return
	}
	safeFilename := sanitizeFilename(filename)
	if safeFilename == "" || safeFilename == "." || safeFilename == ".." {
		writePlain(w, http.StatusBadRequest, "Invalid filename")
		return
	}
	filePath, err := resolveSafePath(dirParam, safeFilename)
	if err != nil {
		writePlain(w, http.StatusBadRequest, err.Error())
		return
	}
	info, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		writePlain(w, http.StatusNotFound, "File not found")
		return
	}
	if err != nil || info.IsDir() {
		writePlain(w, http.StatusBadRequest, "Invalid file")
		return
	}

	encoded := url.PathEscape(safeFilename)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, encoded, encoded))

	f, err := os.Open(filePath)
	if err != nil {
		writePlain(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	defer f.Close()
	_, _ = io.Copy(w, f)
}

func handleDelete(w http.ResponseWriter, r *http.Request, dirParam string) {
	filename := r.URL.Query().Get("filename")
	if filename == "" {
		writePlain(w, http.StatusBadRequest, "Missing filename parameter")
		return
	}
	safeFilename := sanitizeFilename(filename)
	if safeFilename == "" || safeFilename == "." || safeFilename == ".." {
		writePlain(w, http.StatusBadRequest, "Invalid filename")
		return
	}
	filePath, err := resolveSafePath(dirParam, safeFilename)
	if err != nil {
		writePlain(w, http.StatusBadRequest, err.Error())
		return
	}
	info, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		writePlain(w, http.StatusNotFound, "File not found")
		return
	}
	if err != nil {
		writePlain(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if !info.Mode().IsRegular() {
		writePlain(w, http.StatusBadRequest, "Target is not a file")
		return
	}
	if err := os.Remove(filePath); err != nil {
		log.Printf("File delete error: %v", err)
		writePlain(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"filename":  safeFilename,
		"directory": dirParam,
		"message":   "File deleted successfully",
	})
}

type fileEntry struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	Size         *int64 `json:"size"`
	ModifiedTime string `json:"modifiedTime"`
	CreatedTime  string `json:"createdTime"`
}

func handleList(w http.ResponseWriter, r *http.Request, dirParam string) {
	targetDir, err := resolveSafePath(dirParam)
	if err != nil {
		writePlain(w, http.StatusBadRequest, err.Error())
		return
	}
	info, err := os.Stat(targetDir)
	if os.IsNotExist(err) {
		writePlain(w, http.StatusNotFound, "Directory not found")
		return
	}
	if err != nil || !info.IsDir() {
		writePlain(w, http.StatusBadRequest, "Target is not a directory")
		return
	}

	entries, err := os.ReadDir(targetDir)
	if err != nil {
		log.Printf("Directory read error: %v", err)
		writePlain(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}

	list := make([]fileEntry, 0, len(entries))
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			continue
		}
		entry := fileEntry{
			Name:         e.Name(),
			ModifiedTime: fi.ModTime().UTC().Format(time.RFC3339),
		}
		// birthtime: Go's os.FileInfo doesn't expose birthtime cross-platform,
		// so we fall back to ModTime (same behaviour for Linux containers).
		entry.CreatedTime = getBirthtime(fi)
		if e.IsDir() {
			entry.Type = "directory"
		} else {
			entry.Type = "file"
			sz := fi.Size()
			entry.Size = &sz
		}
		list = append(list, entry)
	}

	// Sort: directories first, then alphabetical
	sortFileList(list)

	writeJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"directory":  dirParam,
		"files":      list,
		"totalCount": len(list),
	})
}

// getBirthtime returns the best available approximation of file creation time.
// Go's os.FileInfo does not expose birthtime in a cross-platform way, so
// ModTime is used as a portable fallback.
func getBirthtime(fi os.FileInfo) string {
	return fi.ModTime().UTC().Format(time.RFC3339)
}

func sortFileList(list []fileEntry) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].Type != list[j].Type {
			return list[i].Type == "directory"
		}
		return list[i].Name < list[j].Name
	})
}

// ── Server Books Handlers ──────────────────────────────────────────────────────

type serverBookEntry struct {
	Name         string `json:"name"`
	Format       string `json:"format"`
	Size         int64  `json:"size"`
	ModifiedTime string `json:"modifiedTime"`
	Dir          string `json:"dir"`
}

var supportedBookExts = map[string]bool{
	".epub": true, ".pdf": true, ".mobi": true, ".azw": true, ".azw3": true,
	".txt": true, ".fb2": true, ".cbz": true, ".cbr": true, ".cbt": true,
	".cb7": true, ".html": true, ".htm": true, ".md": true, ".djvu": true,
}

func scanServerBookDir(dir string) []serverBookEntry {
	var list []serverBookEntry
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if !supportedBookExts[ext] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		list = append(list, serverBookEntry{
			Name:         d.Name(),
			Format:       ext[1:],
			Size:         info.Size(),
			ModifiedTime: info.ModTime().UTC().Format(time.RFC3339),
			Dir:          dir,
		})
		return nil
	})
	return list
}

func handleServerBooksList(w http.ResponseWriter, r *http.Request) {
	if len(serverBooksDirs) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"success":    true,
			"books":      []serverBookEntry{},
			"totalCount": 0,
			"message":    "Server books directory not configured",
		})
		return
	}

	list := make([]serverBookEntry, 0)
	for _, dir := range serverBooksDirs {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		list = append(list, scanServerBookDir(dir)...)
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"books":      list,
		"totalCount": len(list),
		"directories": serverBooksDirs,
	})
}

func handleServerBookRead(w http.ResponseWriter, r *http.Request) {
	if len(serverBooksDirs) == 0 {
		writePlain(w, http.StatusBadRequest, "Server books directory not configured")
		return
	}

	filename := r.URL.Query().Get("file")
	if filename == "" {
		writePlain(w, http.StatusBadRequest, "Missing file parameter")
		return
	}

	// Prevent directory traversal
	safeFilename := sanitizeFilename(filename)
	if safeFilename == "" || safeFilename == "." || safeFilename == ".." {
		writePlain(w, http.StatusBadRequest, "Invalid filename")
		return
	}

	// Also reject if sanitized name differs (handles more traversal patterns)
	if safeFilename != filename {
		writePlain(w, http.StatusBadRequest, "Invalid filename")
		return
	}

	// Search in all configured directories (or specific dir if provided)
	specificDir := r.URL.Query().Get("dir")
	dirsToSearch := serverBooksDirs
	if specificDir != "" {
		dirsToSearch = []string{specificDir}
	}

	for _, dir := range dirsToSearch {
		var foundPath string
		filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() && d.Name() == safeFilename {
				foundPath = path
				return filepath.SkipAll
			}
			return nil
		})
		if foundPath == "" {
			continue
		}

		info, err := os.Stat(foundPath)
		if err != nil || info.IsDir() {
			continue
		}

		ext := strings.ToLower(filepath.Ext(safeFilename))
		contentType := bookMime(ext[1:])

		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
		w.Header().Set("Content-Disposition",
			fmt.Sprintf(`inline; filename="%s"`, url.PathEscape(safeFilename)))

		f, err := os.Open(foundPath)
		if err != nil {
			writePlain(w, http.StatusInternalServerError, "Internal Server Error")
			return
		}
		defer f.Close()
		_, _ = io.Copy(w, f)
		return
	}

	writePlain(w, http.StatusNotFound, "File not found")
}

func handleServerBookDelete(w http.ResponseWriter, r *http.Request) {
	if len(serverBooksDirs) == 0 {
		writePlain(w, http.StatusBadRequest, "Server books directory not configured")
		return
	}

	filename := r.URL.Query().Get("file")
	if filename == "" {
		writePlain(w, http.StatusBadRequest, "Missing file parameter")
		return
	}

	safeFilename := sanitizeFilename(filename)
	if safeFilename == "" || safeFilename == "." || safeFilename == ".." {
		writePlain(w, http.StatusBadRequest, "Invalid filename")
		return
	}
	if safeFilename != filename {
		writePlain(w, http.StatusBadRequest, "Invalid filename")
		return
	}

	specificDir := r.URL.Query().Get("dir")
	dirsToSearch := serverBooksDirs
	if specificDir != "" {
		dirsToSearch = []string{specificDir}
	}

	for _, dir := range dirsToSearch {
		var foundPath string
		filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || foundPath != "" {
				return filepath.SkipAll
			}
			if !d.IsDir() && d.Name() == safeFilename {
				foundPath = path
				return filepath.SkipAll
			}
			return nil
		})
		if foundPath == "" {
			continue
		}

		if err := os.Remove(foundPath); err != nil {
			log.Printf("Delete server book error: %v", err)
			writePlain(w, http.StatusInternalServerError, "Failed to delete file")
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"success":  true,
			"filename": safeFilename,
			"message":  "File deleted successfully",
		})
		return
	}

	writePlain(w, http.StatusNotFound, "File not found")
}

// ── Router ────────────────────────────────────────────────────────────────────

func handler(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	serverOrigin := getServerOrigin(r)
	isCrossOrigin := origin != "" && origin != serverOrigin
	corsAllowed := applyCorsHeaders(w, r)

	// Pre-flight
	if r.Method == http.MethodOptions {
		if isCrossOrigin && !corsAllowed {
			writePlain(w, http.StatusForbidden, "Origin not allowed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Block disallowed cross-origin requests
	if isCrossOrigin && !corsAllowed {
		writePlain(w, http.StatusForbidden, "Origin not allowed")
		return
	}

	dirParam := r.URL.Query().Get("dir")
	path := r.URL.Path

	// Server books endpoints
	if r.Method == http.MethodGet && (path == "/api/server-books" || path == "/api/server-books/read") {
		switch {
		case path == "/api/server-books":
			handleServerBooksList(w, r)
		case path == "/api/server-books/read":
			handleServerBookRead(w, r)
		}
		return
	}
	if r.Method == http.MethodDelete && path == "/api/server-books/delete" {
		handleServerBookDelete(w, r)
		return
	}

	switch {
	case r.Method == http.MethodPost && path == "/upload":
		handleUpload(w, r, dirParam)
	case r.Method == http.MethodGet && path == "/download":
		handleDownload(w, r, dirParam)
	case r.Method == http.MethodDelete && path == "/delete":
		handleDelete(w, r, dirParam)
	case r.Method == http.MethodGet && path == "/list":
		handleList(w, r, dirParam)
	case opdsEnabled && (path == "/opds" || path == "/opds/" || strings.HasPrefix(path, "/opds/")):
		opdsHandler(w, r)
	default:
		writePlain(w, http.StatusNotFound, "Not Found")
	}
}

func main() {
	// Initialise KOReader sync server (reads env, opens DB if enabled).
	initKoreader()

	if !serverEnabled && !koreaderEnabled {
		log.Println("All servers are disabled.")
		log.Println("  Set ENABLE_HTTP_SERVER=true  to enable the file server.")
		log.Println("  Set ENABLE_KOREADER_SERVER=true to enable the KOReader sync server.")
		log.Println("  Set ENABLE_OPDS=true to enable the OPDS catalog (requires ENABLE_HTTP_SERVER=true).")
		os.Exit(0)
	}

	if opdsEnabled && !serverEnabled {
		log.Println("Warning: ENABLE_OPDS=true but ENABLE_HTTP_SERVER is not true. OPDS catalog will not be available.")
	}

	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		log.Fatalf("Cannot create uploads directory: %v", err)
	}

	if len(serverBooksDirs) > 0 {
		for _, d := range serverBooksDirs {
			if err := os.MkdirAll(d, 0o755); err != nil {
				log.Printf("Warning: Cannot create server books directory %s: %v", d, err)
			}
		}
		log.Printf("Server books directories: %v", serverBooksDirs)
	}

	// Start KOReader sync server in background if enabled.
	if koreaderEnabled {
		go startKoreaderServer()
	}

	// Start the main file server if enabled.
	if serverEnabled {
		addr := ":" + port
		log.Printf("File Server running at http://localhost%s", addr)
		if opdsEnabled {
			log.Printf("OPDS catalog available at http://localhost%s/opds", addr)
		}

		srv := &http.Server{
			Addr:         addr,
			Handler:      http.HandlerFunc(handler),
			ReadTimeout:  5 * time.Minute, // allow large uploads
			WriteTimeout: 5 * time.Minute,
			IdleTimeout:  60 * time.Second,
		}
		log.Fatal(srv.ListenAndServe())
	} else {
		// Block forever while the KOReader goroutine runs.
		select {}
	}
}
