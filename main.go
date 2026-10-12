// pdfpresenter: browser presenter for Typst projects (or plain PDFs).
//
// Usage:
//
//	pdfpresenter [flags] slides.pdf     # serve a PDF (+ optional slides.pdfpc + slides.times)
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
	"flag"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/Thecrafterja/pdfpresenter/internal/server"
)

//go:embed web
var webFS embed.FS

// version is set via -ldflags "-X main.version=..." at release build time.
var version = "dev"

func init() {
	mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

func main() {
	port := flag.Int("port", 8765, "HTTP port")
	rootFlag := flag.String("root", "", "Typst project root (default: folder of the source file)")
	noOpen := flag.Bool("no-open", false, "don't open the browser")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [flags] [slides.typ|slides.pdf]\n", filepath.Base(os.Args[0]))
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	if flag.NArg() > 1 {
		flag.Usage()
		os.Exit(2)
	}

	assets, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}

	tmpDir, err := os.MkdirTemp("", "pdfpresenter-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	st := server.NewState()
	if flag.NArg() == 1 {
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
		st.SetTarget(src, root)
	}
	go server.Watch(st, tmpDir)

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		log.Fatal(err)
	}
	url := fmt.Sprintf("http://%s/", ln.Addr())
	log.Printf("Presenter view: %s   (Ctrl+C to quit)", url)
	if !*noOpen {
		server.OpenBrowser(url)
	}
	go func() { log.Fatal(http.Serve(ln, server.NewMux(st, assets))) }()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
}
