package tdms_test

import (
	"bytes"
	"fmt"
	"time"

	"github.com/trey-copeland/gotdms"
)

func Example_roundTrip() {
	start := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	buf := &bytes.Buffer{}
	w, err := tdms.NewWriter(buf)
	if err != nil {
		fmt.Println(err)
		return
	}
	if err := w.SetProperty("title", "demo"); err != nil {
		fmt.Println(err)
		return
	}
	ch, err := w.Group("demo").Channel("voltage", tdms.TypeFloat64)
	if err != nil {
		fmt.Println(err)
		return
	}
	if err := ch.SetProperty(tdms.PropUnitString, "V"); err != nil {
		fmt.Println(err)
		return
	}
	if err := ch.SetProperty(tdms.PropIncrement, 0.001); err != nil {
		fmt.Println(err)
		return
	}
	if err := ch.SetProperty(tdms.PropStartTime, start); err != nil {
		fmt.Println(err)
		return
	}
	if err := ch.Write([]float64{1.5, 2.5, 3.5}); err != nil {
		fmt.Println(err)
		return
	}
	if err := w.Close(); err != nil {
		fmt.Println(err)
		return
	}

	f, err := tdms.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		fmt.Println(err)
		return
	}
	defer f.Close()
	g, _ := f.Group("demo")
	c, _ := g.Channel("voltage")
	values, err := c.Float64s()
	if err != nil {
		fmt.Println(err)
		return
	}
	unit, _ := c.Unit()
	inc, _ := c.Increment()
	when, _ := c.StartTime()
	fmt.Println("group:", g.Name())
	fmt.Println("channel:", c.Name())
	fmt.Println("values:", values)
	fmt.Println("unit:", unit)
	fmt.Printf("increment: %.3f\n", inc)
	fmt.Println("start:", when.Format(time.RFC3339))
	// Output:
	// group: demo
	// channel: voltage
	// values: [1.5 2.5 3.5]
	// unit: V
	// increment: 0.001
	// start: 2020-01-02T03:04:05Z
}
