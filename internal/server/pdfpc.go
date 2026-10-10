package server

import (
	"encoding/json"
	"fmt"
	"strings"
)

type pdfpcFile struct {
	Pages []struct {
		Label any    `json:"label"`
		Note  string `json:"note"`
	} `json:"pages"`
}

// ParsePdfpc returns one note per PDF page; overlay subslides inherit the
// note of the previous page if they share its label.
func ParsePdfpc(data []byte) ([]string, error) {
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
