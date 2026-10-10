package server

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var watchExt = map[string]bool{
	".typ": true, ".bib": true, ".csv": true, ".json": true, ".yaml": true, ".yml": true,
	".toml": true, ".xml": true, ".txt": true, ".md": true, ".png": true, ".jpg": true,
	".jpeg": true, ".gif": true, ".svg": true, ".webp": true, ".pdf": true, ".pdfpc": true,
	".ttf": true, ".otf": true,
}

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

func buildTypst(src, root, tmpDir string, st *State) {
	out := filepath.Join(tmpDir, "deck.pdf")
	cmd := exec.Command("typst", "compile", "--root", root, "--format", "pdf", src, out)
	if msg, err := cmd.CombinedOutput(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			st.SetError("`typst` CLI not found on PATH.")
		} else {
			st.SetError(strings.TrimSpace(string(msg)))
		}
		return
	}
	pdf, err := os.ReadFile(out)
	if err != nil {
		st.SetError(err.Error())
		return
	}
	var notes []string
	q := exec.Command("typst", "query", "--root", root, src, "<pdfpc-file>", "--field", "value", "--one")
	if raw, err := q.Output(); err == nil && len(strings.TrimSpace(string(raw))) > 0 {
		if n, err := ParsePdfpc(raw); err == nil {
			notes = n
		}
	}
	st.SetDeck(pdf, notes)
}

func buildPDF(src string, st *State) {
	pdf, err := os.ReadFile(src)
	if err != nil {
		st.SetError(err.Error())
		return
	}
	var notes []string
	pc := strings.TrimSuffix(src, filepath.Ext(src)) + ".pdfpc"
	if raw, err := os.ReadFile(pc); err == nil {
		if n, err := ParsePdfpc(raw); err == nil {
			notes = n
		}
	}
	st.SetDeck(pdf, notes)
}

// Watch polls the active target's project directory for changes and
// triggers rebuilds, writing intermediate Typst output into tmpDir.
func Watch(st *State, tmpDir string) {
	lastGen := -1
	var src, root, pdfpc string
	var isTyp bool
	var last snap
	first := true
	for ; ; time.Sleep(500 * time.Millisecond) {
		curSrc, curRoot, gen := st.Target()
		if curSrc == "" {
			continue
		}
		if gen != lastGen {
			src, root = curSrc, curRoot
			isTyp = strings.EqualFold(filepath.Ext(src), ".typ")
			pdfpc = strings.TrimSuffix(src, filepath.Ext(src)) + ".pdfpc"
			first = true
			lastGen = gen
		}
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
