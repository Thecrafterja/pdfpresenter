package server

import "encoding/json"

type timesFile []struct {
	Idx      int `json:"idx"`
	Duration int `json:"duration"`
}

// ParseTimes returns a per-slide duration budget (seconds), indexed by
// zero-based slide idx. Missing indices default to 0 (no limit).
func ParseTimes(data []byte) ([]int, error) {
	var f timesFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	n := 0
	for _, e := range f {
		if e.Idx+1 > n {
			n = e.Idx + 1
		}
	}
	durations := make([]int, n)
	for _, e := range f {
		if e.Idx >= 0 {
			durations[e.Idx] = e.Duration
		}
	}
	return durations, nil
}
