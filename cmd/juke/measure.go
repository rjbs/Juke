package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"semiotic.systems/juke/internal/audio"
)

// runMeasure prints what the audio package finds out about each file
// named: for an MP3, every way of measuring it, so a length that looks
// wrong can be traced to the header, the estimate or the walk.
func runMeasure(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("juke measure: name at least one file")
	}
	for _, p := range args {
		fmt.Printf("%s\n", p)
		fh, err := os.Open(p)
		if err != nil {
			fmt.Printf("  %v\n", err)
			continue
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".m4a", ".mp4":
			d, err := audio.Duration(fh, "audio/mp4")
			if err != nil {
				fmt.Printf("  %v\n", err)
			} else {
				fmt.Printf("  chosen: %v\n", d)
			}
		default:
			a, err := audio.Analyze(fh)
			if err != nil {
				fmt.Printf("  %v\n", err)
			} else {
				fmt.Print("  " + strings.ReplaceAll(strings.TrimSuffix(a.String(), "\n"), "\n", "\n  ") + "\n")
			}
		}
		fh.Close()
	}
	return nil
}
