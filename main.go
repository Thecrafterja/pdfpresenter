// typst-present: browser presenter for Typst projects (or plain PDFs).
//
// Usage:
//
//	typst-present [flags] slides.typ     # compile + watch a Typst project
//	typst-present [flags] slides.pdf     # serve a PDF (+ optional slides.pdfpc)
//
// Flags must come before the file name:
//
//	-port 8765   -root DIR   -no-open
//
// Needs the `typst` CLI on PATH for .typ input. Speaker notes are read from
// the <pdfpc-file> metadata label (polylux: #pdfpc.speaker-note("...")).
package main

import (
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

//go:embed present.html manifest.webmanifest sw.js icon-192.png icon-512.png
var assets embed.FS

func init() {
	mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

var watchExt = map[string]bool{
	".typ": true, ".bib": true, ".csv": true, ".json": true, ".yaml": true, ".yml": true,
	".toml": true, ".xml": true, ".txt": true, ".md": true, ".png": true, ".jpg": true,
	".jpeg": true, ".gif": true, ".svg": true, ".webp": true, ".pdf": true, ".pdfpc": true,
	".ttf": true, ".otf": true,
}

// ---------- shared state ----------

type state struct {
	mu      sync.RWMutex
	version int
	errMsg  string
	pdf     []byte
	notes   []string
}

func (s *state) setError(msg string) {
	s.mu.Lock()
	s.errMsg = msg
	s.mu.Unlock()
}

func (s *state) setDeck(pdf []byte, notes []string) {
	s.mu.Lock()
	s.errMsg, s.pdf, s.notes = "", pdf, notes
	s.version++
	s.mu.Unlock()
}

// ---------- pdfpc parsing ----------

type pdfpcFile struct {
	Pages []struct {
		Label any    `json:"label"`
		Note  string `json:"note"`
	} `json:"pages"`
}

// parsePdfpc returns one note per PDF page; overlay subslides inherit the
// note of the previous page if they share its label.
func parsePdfpc(data []byte) ([]string, error) {
	var f pdfpcFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	notes := make([]string, 0, len(f.Pages))
	prevLabel, prevNote := "", ""
	for i, p := range f.Pages {
		note := strings.TrimSpace(p.Note)
		label := ""
		if p.Label != nil {
			label = fmt.Sprint(p.Label)
		}
		if note == "" && i > 0 && label != "" && label == prevLabel {
			note = prevNote
		}
		notes = append(notes, note)
		prevLabel, prevNote = label, note
	}
	return notes, nil
}

// ---------- watching ----------

type snap struct {
	newest int64
	count  int
}

func snapshot(root string, extra ...string) snap {
	var s snap
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !watchExt[strings.ToLower(filepath.Ext(name))] {
			return nil
		}
		if info, err := d.Info(); err == nil {
			if t := info.ModTime().UnixNano(); t > s.newest {
				s.newest = t
			}
			s.count++
		}
		return nil
	})
	for _, p := range extra {
		if info, err := os.Stat(p); err == nil {
			if t := info.ModTime().UnixNano(); t > s.newest {
				s.newest = t
			}
			s.count++
		}
	}
	return s
}

func buildTypst(src, root, tmpDir string, st *state) {
	out := filepath.Join(tmpDir, "deck.pdf")
	cmd := exec.Command("typst", "compile", "--root", root, "--format", "pdf", src, out)
	if msg, err := cmd.CombinedOutput(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			st.setError("`typst` CLI not found on PATH.")
		} else {
			st.setError(strings.TrimSpace(string(msg)))
		}
		return
	}
	pdf, err := os.ReadFile(out)
	if err != nil {
		st.setError(err.Error())
		return
	}
	var notes []string
	q := exec.Command("typst", "query", "--root", root, src, "<pdfpc-file>", "--field", "value", "--one")
	if raw, err := q.Output(); err == nil && len(strings.TrimSpace(string(raw))) > 0 {
		if n, err := parsePdfpc(raw); err == nil {
			notes = n
		}
	}
	st.setDeck(pdf, notes)
}

func buildPDF(src string, st *state) {
	pdf, err := os.ReadFile(src)
	if err != nil {
		st.setError(err.Error())
		return
	}
	var notes []string
	pc := strings.TrimSuffix(src, filepath.Ext(src)) + ".pdfpc"
	if raw, err := os.ReadFile(pc); err == nil {
		if n, err := parsePdfpc(raw); err == nil {
			notes = n
		}
	}
	st.setDeck(pdf, notes)
}

func watch(src, root, tmpDir string, st *state) {
	isTyp := strings.EqualFold(filepath.Ext(src), ".typ")
	pdfpc := strings.TrimSuffix(src, filepath.Ext(src)) + ".pdfpc"
	var last snap
	first := true
	for ; ; time.Sleep(500 * time.Millisecond) {
		var cur snap
		if isTyp {
			cur = snapshot(root, pdfpc)
		} else {
			cur = snapshot(filepath.Dir(src), pdfpc) // cheap enough for a slides folder
		}
		if !first && cur == last {
			continue
		}
		first, last = false, cur
		log.Println("[build] change detected")
		if isTyp {
			buildTypst(src, root, tmpDir, st)
		} else {
			buildPDF(src, st)
		}
		st.mu.RLock()
		if st.errMsg != "" {
			log.Printf("[build] error:\n%s", st.errMsg)
		}
		st.mu.RUnlock()
	}
}

// ---------- HTTP ----------

func noStore(w http.ResponseWriter) { w.Header().Set("Cache-Control", "no-store") }

func newMux(st *state) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			http.NotFound(w, r)
			return
		}
		indexHTML, err := assets.ReadFile("present.html")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		noStore(w)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	})

	assetServer := http.FileServer(http.FS(assets))
	for _, path := range []string{"/manifest.webmanifest", "/sw.js", "/icon-192.png", "/icon-512.png"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			noStore(w)
			assetServer.ServeHTTP(w, r)
		})
	}

	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		st.mu.RLock()
		body := map[string]any{"version": st.version, "error": st.errMsg, "has_pdf": st.pdf != nil}
		st.mu.RUnlock()
		noStore(w)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
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

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func main() {
	port := flag.Int("port", 8765, "HTTP port")
	rootFlag := flag.String("root", "", "Typst project root (default: folder of the source file)")
	noOpen := flag.Bool("no-open", false, "don't open the browser")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [flags] slides.typ|slides.pdf\n", filepath.Base(os.Args[0]))
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	src, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}
	if _, err := os.Stat(src); err != nil {
		log.Fatalf("%s: %v", src, err)
	}
	root := filepath.Dir(src)
	if *rootFlag != "" {
		if root, err = filepath.Abs(*rootFlag); err != nil {
			log.Fatal(err)
		}
	}

	tmpDir, err := os.MkdirTemp("", "typst-present-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	st := &state{}
	go watch(src, root, tmpDir, st)

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		log.Fatal(err)
	}
	url := fmt.Sprintf("http://%s/", ln.Addr())
	log.Printf("Presenter view: %s   (Ctrl+C to quit)", url)
	if !*noOpen {
		openBrowser(url)
	}
	go func() { log.Fatal(http.Serve(ln, newMux(st))) }()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
}
