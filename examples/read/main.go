// Command read prints groups, channels, and the first few samples of a TDMS file.
// With no arguments it reads sample.tdms from the current directory.
package main

import (
	"fmt"
	"os"

	"github.com/trey-copeland/gotdms"
)

func main() {
	path := "sample.tdms"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	f, err := tdms.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	fmt.Printf("version %d, %d segment(s)\n", f.Version(), f.SegmentCount())
	for _, p := range f.Properties() {
		fmt.Printf("file %s = %v\n", p.Name, p.Value)
	}
	for _, g := range f.Groups() {
		fmt.Printf("group %s\n", g.Name())
		for _, c := range g.Channels() {
			fmt.Printf("  channel %s type %s len %d\n", c.Name(), c.DataType(), c.Len())
			if u, ok := c.Unit(); ok {
				fmt.Printf("    unit %s\n", u)
			}
			if inc, ok := c.Increment(); ok {
				fmt.Printf("    increment %g\n", inc)
			}
			if c.Len() == 0 || c.DataType() != tdms.TypeFloat64 {
				continue
			}
			cur := c.Cursor(3)
			if cur.Next() {
				fmt.Printf("    first batch %v\n", cur.Values())
			}
			if err := cur.Err(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
	}
}
