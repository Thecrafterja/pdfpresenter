package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func noStore(w http.ResponseWriter) { w.Header().Set("Cache-Control", "no-store") }

// NewMux builds the HTTP handler for the presenter app. assets is the web/
// directory (present.html, style.css, app.js, manifest, icons, sw.js).
func NewMux(st *State, assets fs.FS) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			http.NotFound(w, r)
			return
		}
		indexHTML, err := fs.ReadFile(assets, "present.html")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		noStore(w)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	})

	assetServer := http.FileServer(http.FS(assets))
	for _, path := range []string{
		"/style.css", "/app.js", "/manifest.webmanifest", "/sw.js", "/icon-192.png", "/icon-512.png",
	} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			noStore(w)
			assetServer.ServeHTTP(w, r)
		})
	}

	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		st.mu.RLock()
		body := map[string]any{
			"version": st.version, "gen": st.gen, "file": st.src, "root": st.root,
			"error": st.errMsg, "has_pdf": st.pdf != nil,
		}
		st.mu.RUnlock()
		noStore(w)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})

	mux.HandleFunc("/browse", func(w http.ResponseWriter, r *http.Request) {
		noStore(w)
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Query().Get("path")
		if path == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
			path = home
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "not a directory"})
			return
		}
		dirEntries, err := os.ReadDir(path)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		type entry struct {
			Name       string `json:"name"`
			IsDir      bool   `json:"is_dir"`
			Selectable bool   `json:"selectable,omitempty"`
		}
		var dirs, files []entry
		for _, d := range dirEntries {
			name := d.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if d.IsDir() {
				dirs = append(dirs, entry{Name: name, IsDir: true})
				continue
			}
			ext := strings.ToLower(filepath.Ext(name))
			if ext == ".typ" || ext == ".pdf" {
				files = append(files, entry{Name: name, Selectable: true})
			}
		}
		sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
		sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
		entries := append(dirs, files...)
		parent := filepath.Dir(path)
		if parent == path {
			parent = ""
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"path": path, "parent": parent, "entries": entries})
	})

	mux.HandleFunc("/open", func(w http.ResponseWriter, r *http.Request) {
		noStore(w)
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "missing path"})
			return
		}
		path, err := filepath.Abs(body.Path)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
			return
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "not a file"})
			return
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".typ" && ext != ".pdf" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "unsupported file type"})
			return
		}
		root := filepath.Dir(path)
		st.SetTarget(path, root)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "src": path, "root": root})
	})

	mux.HandleFunc("/notes.json", func(w http.ResponseWriter, r *http.Request) {
		st.mu.RLock()
		notes := st.notes
		st.mu.RUnlock()
		if notes == nil {
			notes = []string{}
		}
		noStore(w)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"notes": notes})
	})

	mux.HandleFunc("/deck.pdf", func(w http.ResponseWriter, r *http.Request) {
		st.mu.RLock()
		pdf := st.pdf
		st.mu.RUnlock()
		if pdf == nil {
			http.Error(w, "no pdf yet", http.StatusNotFound)
			return
		}
		noStore(w)
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(pdf)
	})

	return mux
}
