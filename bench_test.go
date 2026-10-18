package tdms

import (
	"bytes"
	"testing"
)

func BenchmarkWriteFloat64(b *testing.B) {
	const n = 100000
	data := make([]float64, n)
	for i := range data {
		data[i] = float64(i)
	}
	b.SetBytes(int64(n) * 8)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := &bytes.Buffer{}
		w, err := NewWriter(buf)
		if err != nil {
			b.Fatal(err)
		}
		ch, err := w.Group("g").Channel("c", TypeFloat64)
		if err != nil {
			b.Fatal(err)
		}
		if err := ch.Write(data); err != nil {
			b.Fatal(err)
		}
		if err := w.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReadFloat64(b *testing.B) {
	const n = 100000
	data := make([]float64, n)
	buf := &bytes.Buffer{}
	w, err := NewWriter(buf)
	if err != nil {
		b.Fatal(err)
	}
	ch, err := w.Group("g").Channel("c", TypeFloat64)
	if err != nil {
		b.Fatal(err)
	}
	if err := ch.Write(data); err != nil {
		b.Fatal(err)
	}
	if err := w.Close(); err != nil {
		b.Fatal(err)
	}
	raw := append([]byte(nil), buf.Bytes()...)
	b.SetBytes(int64(n) * 8)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f, err := NewReader(bytes.NewReader(raw))
		if err != nil {
			b.Fatal(err)
		}
		g, _ := f.Group("g")
		c, _ := g.Channel("c")
		if _, err := c.Float64s(); err != nil {
			b.Fatal(err)
		}
		f.Close()
	}
}

func BenchmarkStreamFloat64(b *testing.B) {
	const n = 100000
	data := make([]float64, n)
	buf := &bytes.Buffer{}
	w, err := NewWriter(buf)
	if err != nil {
		b.Fatal(err)
	}
	ch, err := w.Group("g").Channel("c", TypeFloat64)
	if err != nil {
		b.Fatal(err)
	}
	if err := ch.Write(data); err != nil {
		b.Fatal(err)
	}
	if err := w.Close(); err != nil {
		b.Fatal(err)
	}
	raw := append([]byte(nil), buf.Bytes()...)
	b.SetBytes(int64(n) * 8)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f, err := NewReader(bytes.NewReader(raw))
		if err != nil {
			b.Fatal(err)
		}
		g, _ := f.Group("g")
		c, _ := g.Channel("c")
		cur := c.Cursor(4096)
		var count int
		for cur.Next() {
			count += cur.Len()
		}
		if err := cur.Err(); err != nil {
			b.Fatal(err)
		}
		if count != n {
			b.Fatalf("count %d", count)
		}
		f.Close()
	}
}
