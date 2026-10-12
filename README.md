# PDF Presenter
PDF Presenter is a small CLI tool that is build for presenting pdfs, especially those made by my Typst template (link is about to follow). The whole UI runs inside your browser and is split into two parts: A presenter's view like you are used to from other presentation software, and a slides view.

## Install

### Homebrew (macOS)
```sh
brew install Thecrafterja/presenter/presenter
```

### Manual download
Download the binary matching your architecture from [GitHub Releases](https://github.com/Thecrafterja/pdfpresenter/releases), and then execute it via `./presenter <slides-pdf>`.

There are additional files that are also read by pdfpresenter if existing under the same name in the same directory:
* `slides.pdfpc`: This is a standard origintaing from the pdfpc tool, which contains notes for each slide. They are shown in pdfpresenter's presenter view.
* `slides.times`: This document contains the planned time for each slide. If this document is provided, the slide timer in the presenter view will be color coded according to the time limit.
