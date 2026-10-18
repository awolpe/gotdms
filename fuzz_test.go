package tdms

import (
	"bytes"
	"testing"
)

func FuzzReader(f *testing.F) {
	f.Add([]byte("TDSm"))
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte{0xff}, 64))
	// A minimal root-only file, produced without depending on a previous run.
	var buf bytes.Buffer
	w, err := NewWriter(&buf)
	if err != nil {
		f.Fatal(err)
	}
	if err := w.Close(); err != nil {
		f.Fatal(err)
	}
	f.Add(buf.Bytes())

	f.Fuzz(func(t *testing.T, data []byte) {
		file, err := NewReader(bytes.NewReader(data))
		if err != nil {
			return
		}
		defer file.Close()
		_ = file.Version()
		_ = file.Properties()
		for _, g := range file.Groups() {
			_ = g.Properties()
			for _, ch := range g.Channels() {
				if ch.Len() > 1<<20 {
					t.Fatalf("channel %s claims %d values in a %d byte input", ch.Path(), ch.Len(), len(data))
				}
				_, _ = ch.Values()
				_, _ = ch.Float64s()
				cur := ch.Cursor(3)
				for i := 0; cur.Next() && i < 8; i++ {
					_ = cur.Values()
				}
				_ = cur.Err()
			}
		}
	})
}
