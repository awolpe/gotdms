// Command write creates sample.tdms in the current directory.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/trey-copeland/gotdms"
)

func main() {
	const path = "sample.tdms"
	w, err := tdms.Create(path)
	if err != nil {
		fatal(err)
	}
	if err := w.SetProperty("title", "furnace run 14"); err != nil {
		fatal(err)
	}
	ch, err := w.Group("acquisition").Channel("temperature", tdms.TypeFloat64)
	if err != nil {
		fatal(err)
	}
	start := time.Date(2024, 3, 1, 8, 0, 0, 0, time.UTC)
	if err := ch.SetProperty(tdms.PropUnitString, "degC"); err != nil {
		fatal(err)
	}
	if err := ch.SetProperty(tdms.PropIncrement, 0.1); err != nil {
		fatal(err)
	}
	if err := ch.SetProperty(tdms.PropStartTime, start); err != nil {
		fatal(err)
	}
	if err := ch.Write([]float64{21.5, 21.7, 22.0, 22.4}); err != nil {
		fatal(err)
	}
	// A second flush is a second segment. The index is reused when the layout matches.
	if err := w.Flush(); err != nil {
		fatal(err)
	}
	if err := ch.Write([]float64{22.8, 23.1}); err != nil {
		fatal(err)
	}
	if err := w.Close(); err != nil {
		fatal(err)
	}
	fmt.Println("wrote", path)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
