# CLAUDE.md
This repo contains the source code for a web based presenter tool. The tool can mainly present PDF files generated from Typst. We have a presenter view and a slide view.

We accept the following file combination:
- .pdf for the actual slides
- .pdfpc for the presenter's notes
- .times for the times that are planned for every slide

## Tech Stack
For the backend we use Go (GoLang). For the frontend, we use JavaScript without a framework.

## Release process
For providing releases easily, we use homebrew. Whenever a release is ready, we tag the commit and then push it to a branch named after the version tag (e. g. v0.3.1). Then a github workflow automatically builds the new homebrew package and pushes it to the corresponding repository.
