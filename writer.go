package tdms

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"time"
)

// Writer appends TDMS segments to an io.Writer.
//
// Data and property changes are buffered until Flush or Close. Each flush
// emits one segment containing one chunk: every Write on a channel since the
// previous flush is concatenated into that chunk. Channels are laid out in
// the order they were created, not the order of the Write calls.
//
// A seekable writer (os.File, any io.WriteSeeker) first publishes a lead-in
// whose next-segment offset is 0xFFFFFFFFFFFFFFFF — the NI incomplete-segment
// sentinel — then patches the real length after the raw bytes are written. A
// crash in between still leaves a segment the reader can consume up to EOF.
// A writer that cannot seek buffers the finished segment and writes it once.
// Either way, one segment is held in memory until Flush; flush often when
// channels are large.
//
// Segments are little-endian version 4713. SetInterleaved selects the layout
// of later flushes. The writer is not safe for concurrent use.
type Writer struct {
	w        io.Writer
	ws       io.WriteSeeker
	pos      int64
	seekable bool
	own      io.Closer
	path     string

	interleaved bool
	closed      bool
	started     bool

	root      *wObj
	groups    []*wGroup
	gindex    map[string]*wGroup
	prevOrder []string
	prevIndex map[string]wIndex
}

type wIndex struct {
	dt    DataType
	n     uint64
	total uint64
}

type wObj struct {
	path    string
	props   []Property
	dirty   bool
	emitted bool
}

type wGroup struct {
	name     string
	obj      *wObj
	channels []*wChannel
	cindex   map[string]*wChannel
}

type wChannel struct {
	group string
	name  string
	path  string
	dt    DataType
	obj   *wObj
	raw   []byte
	strs  []string
	n     int
}

// GroupWriter is the write handle for one group.
type GroupWriter struct {
	w *Writer
	g *wGroup
}

// ChannelWriter is the write handle for one channel.
type ChannelWriter struct {
	w  *Writer
	ch *wChannel
}

// Create creates or truncates path and returns a writer. Close flushes the
// last segment and closes the file.
func Create(path string) (*Writer, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, wrap("create", path, err)
	}
	w, err := NewWriter(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	w.own = f
	w.path = path
	return w, nil
}

// NewWriter writes a TDMS file to w. If w implements io.WriteSeeker the
// lead-in is patched in place; otherwise each segment is buffered until it
// is complete. NewWriter does not close w.
func NewWriter(w io.Writer) (*Writer, error) {
	if w == nil {
		return nil, wrap("write", "", fmt.Errorf("%w: nil writer", ErrArgument))
	}
	wr := &Writer{
		w:         w,
		root:      &wObj{path: "/"},
		gindex:    map[string]*wGroup{},
		prevIndex: map[string]wIndex{},
	}
	if ws, ok := w.(io.WriteSeeker); ok {
		pos, err := ws.Seek(0, io.SeekCurrent)
		if err == nil {
			wr.ws = ws
			wr.pos = pos
			wr.seekable = true
		}
	}
	return wr, nil
}

// SetInterleaved selects the raw layout used by subsequent Flush calls.
// Interleaved segments require every channel in the chunk to have the same
// number of values and a fixed-size type. A chunk that is only a single
// string channel may be interleaved; the bytes are the ordinary string
// offset table, because that is the only layout the format defines for
// strings. Several string channels, or a string mixed with numeric channels,
// return ErrLayout.
func (w *Writer) SetInterleaved(on bool) { w.interleaved = on }

// SetProperty sets a file-level property. Integers that fit in int32 are
// stored as int32; larger ints are int64. time.Time is stored as a TDMS
// timestamp. The value is written on the next flush that emits metadata.
func (w *Writer) SetProperty(name string, value any) error {
	if w.closed {
		return wrap("write", "/", ErrClosed)
	}
	return w.root.set(name, value)
}

// Group returns the group, creating it if needed.
func (w *Writer) Group(name string) *GroupWriter {
	g, ok := w.gindex[name]
	if !ok {
		g = &wGroup{
			name:   name,
			obj:    &wObj{path: groupPath(name)},
			cindex: map[string]*wChannel{},
		}
		w.gindex[name] = g
		w.groups = append(w.groups, g)
	}
	return &GroupWriter{w: w, g: g}
}

// Name is the group name.
func (g *GroupWriter) Name() string { return g.g.name }

// SetProperty sets a group property.
func (g *GroupWriter) SetProperty(name string, value any) error {
	if g.w.closed {
		return wrap("write", g.g.obj.path, ErrClosed)
	}
	return g.g.obj.set(name, value)
}

// Channel returns the channel, creating it on first use. A second call with
// a different data type returns ErrType.
func (g *GroupWriter) Channel(name string, dt DataType) (*ChannelWriter, error) {
	if g.w.closed {
		return nil, wrap("write", channelPath(g.g.name, name), ErrClosed)
	}
	if dt != TypeString && dt != TypeVoid {
		if _, ok := dt.Size(); !ok || dt == TypeFixedPoint || dt == TypeDAQmxRaw {
			return nil, wrap("write", channelPath(g.g.name, name), fmt.Errorf("%w: %s", ErrUnsupported, dt))
		}
	}
	if ch, ok := g.g.cindex[name]; ok {
		if ch.dt != dt {
			return nil, wrapf("write", ch.path, ErrType, "channel already has type %s", ch.dt)
		}
		return &ChannelWriter{w: g.w, ch: ch}, nil
	}
	ch := &wChannel{
		group: g.g.name,
		name:  name,
		path:  channelPath(g.g.name, name),
		dt:    dt,
		obj:   &wObj{path: channelPath(g.g.name, name)},
	}
	g.g.cindex[name] = ch
	g.g.channels = append(g.g.channels, ch)
	return &ChannelWriter{w: g.w, ch: ch}, nil
}

// Name is the channel name.
func (c *ChannelWriter) Name() string { return c.ch.name }

// DataType is the type given when the channel was created.
func (c *ChannelWriter) DataType() DataType { return c.ch.dt }

// SetProperty sets a channel property.
func (c *ChannelWriter) SetProperty(name string, value any) error {
	if c.w.closed {
		return wrap("write", c.ch.path, ErrClosed)
	}
	return c.ch.obj.set(name, value)
}

// Write appends values to the current segment. values is a slice whose
// element type matches the channel: []int8, []int16, []int32, []int64,
// []uint8, []uint16, []uint32, []uint64, []float32, []float64, []bool,
// []string, []Timestamp or []time.Time, []Extended, []complex64, []complex128.
// Float-with-unit channels take the same slices as the underlying float.
// Repeated calls before Flush belong to one chunk.
func (c *ChannelWriter) Write(values any) error {
	if c.w.closed {
		return wrap("write", c.ch.path, ErrClosed)
	}
	n, raw, strs, err := encodeChannel(c.ch.dt, values)
	if err != nil {
		return wrap("write", c.ch.path, err)
	}
	c.ch.raw = append(c.ch.raw, raw...)
	c.ch.strs = append(c.ch.strs, strs...)
	c.ch.n += n
	return nil
}

// Flush writes the current segment. It is a no-op when nothing has changed
// and a segment has already been written.
func (w *Writer) Flush() error {
	if w.closed {
		return wrap("flush", w.path, ErrClosed)
	}
	data := w.dataChannels()
	if len(data) == 0 && !w.pendingMeta() {
		if !w.started {
			return w.writeEmpty()
		}
		return nil
	}
	if w.interleaved && len(data) > 0 {
		if err := checkInterleaved(data); err != nil {
			return wrap("flush", w.path, err)
		}
	}
	raw, err := buildRaw(data, w.interleaved)
	if err != nil {
		return wrap("flush", w.path, err)
	}
	meta, toc, order, indexes, err := w.buildMeta(data, len(raw) > 0)
	if err != nil {
		return wrap("flush", w.path, err)
	}
	if len(raw) > 0 {
		toc |= TocRawData
		if w.interleaved {
			toc |= TocInterleavedData
		}
	}
	if err := w.writeSegment(toc, meta, raw); err != nil {
		return wrap("flush", w.path, err)
	}
	w.started = true
	w.markEmitted(meta != nil || len(raw) > 0)
	if len(raw) > 0 {
		w.prevOrder = order
		w.prevIndex = indexes
	}
	w.resetData()
	return nil
}

// Close flushes pending data and, when Create was used, closes the file.
// An empty writer still produces a valid one-segment file that contains the
// root object.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	err := w.Flush()
	w.closed = true
	if w.own != nil {
		if cerr := w.own.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		return err
	}
	return nil
}

func (w *Writer) writeEmpty() error {
	meta, err := encodeObjects([]objRec{{
		path: "/",
		mode: idxNone,
	}})
	if err != nil {
		return err
	}
	toc := TocMetaData | TocNewObjList
	if err := w.writeSegment(toc, meta, nil); err != nil {
		return wrap("flush", w.path, err)
	}
	w.started = true
	w.root.emitted = true
	w.root.dirty = false
	return nil
}

func (w *Writer) writeSegment(toc uint32, meta, raw []byte) error {
	if w.seekable {
		// Publish the incomplete-segment sentinel first so a crash before the
		// patch still satisfies the NI rule for the last segment.
		lead := encodeLeadIn(toc, incompleteSegment, uint64(len(meta)))
		if _, err := w.ws.Write(lead); err != nil {
			return err
		}
		if _, err := w.ws.Write(meta); err != nil {
			return err
		}
		if _, err := w.ws.Write(raw); err != nil {
			return err
		}
		if _, err := w.ws.Seek(w.pos+12, io.SeekStart); err != nil {
			return err
		}
		var next [8]byte
		binary.LittleEndian.PutUint64(next[:], uint64(len(meta)+len(raw)))
		if _, err := w.ws.Write(next[:]); err != nil {
			return err
		}
		end := w.pos + int64(leadInSize+len(meta)+len(raw))
		if _, err := w.ws.Seek(end, io.SeekStart); err != nil {
			return err
		}
		w.pos = end
		return nil
	}
	lead := encodeLeadIn(toc, uint64(len(meta)+len(raw)), uint64(len(meta)))
	if _, err := w.w.Write(lead); err != nil {
		return err
	}
	if _, err := w.w.Write(meta); err != nil {
		return err
	}
	if _, err := w.w.Write(raw); err != nil {
		return err
	}
	w.pos += int64(leadInSize + len(meta) + len(raw))
	return nil
}

func (w *Writer) dataChannels() []*wChannel {
	var out []*wChannel
	for _, g := range w.groups {
		for _, ch := range g.channels {
			if ch.n > 0 {
				out = append(out, ch)
			}
		}
	}
	return out
}

func (w *Writer) pendingMeta() bool {
	if w.root.dirty || (!w.root.emitted && !w.started) {
		return true
	}
	for _, g := range w.groups {
		if g.obj.dirty || !g.obj.emitted {
			return true
		}
		for _, ch := range g.channels {
			if ch.obj.dirty || !ch.obj.emitted {
				return true
			}
		}
	}
	return false
}

func (w *Writer) buildMeta(data []*wChannel, hasRaw bool) ([]byte, uint32, []string, map[string]wIndex, error) {
	order := make([]string, len(data))
	for i, ch := range data {
		order[i] = ch.path
	}
	first := !w.started
	newList := first || !prefixExtend(w.prevOrder, order)
	// A segment with no raw data does not change channel order, so it does
	// not need kTocNewObjList after the file has started.
	if !hasRaw && !first {
		newList = false
	}
	var recs []objRec
	if first || w.root.dirty || !w.root.emitted {
		recs = append(recs, objRec{path: "/", mode: idxNone, props: w.root.props})
	}
	for _, g := range w.groups {
		if first || g.obj.dirty || !g.obj.emitted {
			recs = append(recs, objRec{path: g.obj.path, mode: idxNone, props: g.obj.props})
		}
	}
	indexes := map[string]wIndex{}
	for k, v := range w.prevIndex {
		indexes[k] = v
	}
	dataSet := map[string]*wChannel{}
	for _, ch := range data {
		dataSet[ch.path] = ch
	}
	var chans []*wChannel
	if first {
		for _, g := range w.groups {
			chans = append(chans, g.channels...)
		}
	} else if newList {
		chans = append(chans, data...)
		for _, g := range w.groups {
			for _, ch := range g.channels {
				if _, ok := dataSet[ch.path]; ok {
					continue
				}
				if ch.obj.dirty || !ch.obj.emitted {
					chans = append(chans, ch)
				}
			}
		}
	} else {
		for _, g := range w.groups {
			for _, ch := range g.channels {
				_, inData := dataSet[ch.path]
				_, had := w.prevIndex[ch.path]
				total, err := channelTotal(ch)
				if err != nil {
					return nil, 0, nil, nil, err
				}
				changed := inData && (!had || w.prevIndex[ch.path] != (wIndex{dt: ch.dt, n: uint64(ch.n), total: total}))
				if ch.obj.dirty || !ch.obj.emitted || changed || (inData && !containsStr(w.prevOrder, ch.path)) {
					chans = append(chans, ch)
				}
			}
		}
	}
	for _, ch := range chans {
		mode := idxNone
		var total uint64
		if ch.n > 0 {
			var err error
			total, err = channelTotal(ch)
			if err != nil {
				return nil, 0, nil, nil, err
			}
			prev, had := w.prevIndex[ch.path]
			cur := wIndex{dt: ch.dt, n: uint64(ch.n), total: total}
			if had && prev == cur {
				mode = idxMatch
			} else {
				mode = idxFull
			}
			indexes[ch.path] = cur
		} else if !hasRaw {
			if _, had := w.prevIndex[ch.path]; had {
				mode = idxMatch
			}
		} else {
			delete(indexes, ch.path)
		}
		props := []Property(nil)
		if ch.obj.dirty || !ch.obj.emitted {
			props = ch.obj.props
		}
		// newList must name every data channel even when the index matches
		// and the properties are unchanged, so the reader can rebuild order.
		if mode == idxMatch && len(props) == 0 && !newList {
			continue
		}
		recs = append(recs, objRec{
			path:  ch.path,
			mode:  mode,
			dt:    ch.dt,
			n:     uint64(ch.n),
			total: total,
			props: props,
		})
	}
	if hasRaw {
		// Drop indexes for channels that are no longer in the raw order.
		keep := map[string]bool{}
		for _, p := range order {
			keep[p] = true
		}
		for p := range indexes {
			if !keep[p] {
				delete(indexes, p)
			}
		}
	} else {
		indexes = nil
	}
	toc := uint32(0)
	if len(recs) > 0 {
		toc |= TocMetaData
	}
	if newList {
		toc |= TocNewObjList
	}
	if len(recs) == 0 {
		return nil, toc, order, indexes, nil
	}
	meta, err := encodeObjects(recs)
	if err != nil {
		return nil, 0, nil, nil, err
	}
	return meta, toc, order, indexes, nil
}

func (w *Writer) markEmitted(wrote bool) {
	if !wrote {
		return
	}
	w.root.emitted = true
	w.root.dirty = false
	for _, g := range w.groups {
		g.obj.emitted = true
		g.obj.dirty = false
		for _, ch := range g.channels {
			ch.obj.emitted = true
			ch.obj.dirty = false
		}
	}
}

func (w *Writer) resetData() {
	for _, g := range w.groups {
		for _, ch := range g.channels {
			ch.raw = ch.raw[:0]
			ch.strs = nil
			ch.n = 0
		}
	}
}

type objRec struct {
	path  string
	mode  int
	dt    DataType
	n     uint64
	total uint64
	props []Property
}

func encodeObjects(recs []objRec) ([]byte, error) {
	var b []byte
	b = appendU32(b, uint32(len(recs)))
	for _, r := range recs {
		b = appendU32(b, uint32(len(r.path)))
		b = append(b, r.path...)
		switch r.mode {
		case idxNone:
			b = appendU32(b, RawIndexNoData)
		case idxMatch:
			b = appendU32(b, RawIndexMatchesPrevious)
		default:
			if r.dt == TypeString {
				b = appendU32(b, 28)
				b = appendU32(b, uint32(r.dt))
				b = appendU32(b, 1)
				b = appendU64(b, r.n)
				b = appendU64(b, r.total)
			} else {
				b = appendU32(b, 20)
				b = appendU32(b, uint32(r.dt))
				b = appendU32(b, 1)
				b = appendU64(b, r.n)
			}
		}
		b = appendU32(b, uint32(len(r.props)))
		for _, p := range r.props {
			enc, err := encodeProperty(p)
			if err != nil {
				return nil, err
			}
			b = append(b, enc...)
		}
	}
	return b, nil
}

func channelTotal(ch *wChannel) (uint64, error) {
	if ch.dt != TypeString {
		return 0, nil
	}
	return stringBlockSize(ch.strs)
}

func checkInterleaved(data []*wChannel) error {
	n := data[0].n
	strings := 0
	for _, ch := range data {
		if ch.n != n {
			return fmt.Errorf("%w: channel %s has %d values, channel %s has %d", ErrLayout, data[0].path, n, ch.path, ch.n)
		}
		if ch.dt == TypeString {
			strings++
			continue
		}
		if _, ok := ch.dt.Size(); !ok {
			return fmt.Errorf("%w: %s has no fixed size", ErrLayout, ch.dt)
		}
	}
	if strings > 0 && (strings != len(data) || strings > 1) {
		return fmt.Errorf("%w: string data uses a per-channel offset table and cannot be multiplexed with other channels", ErrLayout)
	}
	return nil
}

func buildRaw(data []*wChannel, interleaved bool) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if !interleaved || len(data) == 1 {
		var raw []byte
		var err error
		for _, ch := range data {
			if ch.dt == TypeString {
				raw, err = appendStringBlock(raw, ch.strs)
				if err != nil {
					return nil, err
				}
				continue
			}
			raw = append(raw, ch.raw...)
		}
		return raw, nil
	}
	n := data[0].n
	sizes := make([]int, len(data))
	stride := 0
	for i, ch := range data {
		sz, ok := ch.dt.Size()
		if !ok {
			return nil, ErrLayout
		}
		sizes[i] = sz
		stride += sz
	}
	raw := make([]byte, n*stride)
	for i := 0; i < n; i++ {
		dst := i * stride
		for c, ch := range data {
			sz := sizes[c]
			src := i * sz
			copy(raw[dst:dst+sz], ch.raw[src:src+sz])
			dst += sz
		}
	}
	return raw, nil
}

func prefixExtend(prev, cur []string) bool {
	if len(cur) < len(prev) {
		return false
	}
	for i := range prev {
		if prev[i] != cur[i] {
			return false
		}
	}
	return true
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func (o *wObj) set(name string, value any) error {
	p, err := normalizeProp(name, value)
	if err != nil {
		return wrap("write", o.path, err)
	}
	upsert(&o.props, p)
	o.dirty = true
	return nil
}

func normalizeProp(name string, value any) (Property, error) {
	switch v := value.(type) {
	case int:
		if int64(v) >= math.MinInt32 && int64(v) <= math.MaxInt32 {
			return Property{Name: name, Type: TypeInt32, Value: int32(v)}, nil
		}
		return Property{Name: name, Type: TypeInt64, Value: int64(v)}, nil
	case uint:
		if uint64(v) <= math.MaxUint32 {
			return Property{Name: name, Type: TypeUint32, Value: uint32(v)}, nil
		}
		return Property{Name: name, Type: TypeUint64, Value: uint64(v)}, nil
	case int8:
		return Property{Name: name, Type: TypeInt8, Value: v}, nil
	case int16:
		return Property{Name: name, Type: TypeInt16, Value: v}, nil
	case int32:
		return Property{Name: name, Type: TypeInt32, Value: v}, nil
	case int64:
		return Property{Name: name, Type: TypeInt64, Value: v}, nil
	case uint8:
		return Property{Name: name, Type: TypeUint8, Value: v}, nil
	case uint16:
		return Property{Name: name, Type: TypeUint16, Value: v}, nil
	case uint32:
		return Property{Name: name, Type: TypeUint32, Value: v}, nil
	case uint64:
		return Property{Name: name, Type: TypeUint64, Value: v}, nil
	case float32:
		return Property{Name: name, Type: TypeFloat32, Value: v}, nil
	case float64:
		return Property{Name: name, Type: TypeFloat64, Value: v}, nil
	case bool:
		return Property{Name: name, Type: TypeBool, Value: v}, nil
	case string:
		return Property{Name: name, Type: TypeString, Value: v}, nil
	case time.Time:
		return Property{Name: name, Type: TypeTimestamp, Value: NewTimestamp(v)}, nil
	case Timestamp:
		return Property{Name: name, Type: TypeTimestamp, Value: v}, nil
	case Extended:
		return Property{Name: name, Type: TypeExtendedFloat, Value: v}, nil
	case complex64:
		return Property{Name: name, Type: TypeComplex64, Value: v}, nil
	case complex128:
		return Property{Name: name, Type: TypeComplex128, Value: v}, nil
	default:
		return Property{}, fmt.Errorf("%w: property %q has Go type %T", ErrType, name, value)
	}
}

func encodeChannel(dt DataType, values any) (int, []byte, []string, error) {
	if values == nil {
		return 0, nil, nil, nil
	}
	switch dt.storage() {
	case TypeInt8:
		s, ok := values.([]int8)
		if !ok {
			return 0, nil, nil, typeErr(dt, values)
		}
		return len(s), append([]byte(nil), bytesOfInt8(s)...), nil, nil
	case TypeUint8:
		s, ok := values.([]uint8)
		if !ok {
			return 0, nil, nil, typeErr(dt, values)
		}
		return len(s), append([]byte(nil), s...), nil, nil
	case TypeBool:
		s, ok := values.([]bool)
		if !ok {
			return 0, nil, nil, typeErr(dt, values)
		}
		b := make([]byte, len(s))
		for i, v := range s {
			if v {
				b[i] = 1
			}
		}
		return len(s), b, nil, nil
	case TypeInt16:
		return pack16(values, true, dt)
	case TypeUint16:
		return pack16(values, false, dt)
	case TypeInt32:
		return pack32(values, dt, func(v any) ([]uint32, bool) {
			s, ok := v.([]int32)
			if !ok {
				return nil, false
			}
			out := make([]uint32, len(s))
			for i := range s {
				out[i] = uint32(s[i])
			}
			return out, true
		})
	case TypeUint32:
		return pack32(values, dt, func(v any) ([]uint32, bool) {
			s, ok := v.([]uint32)
			if !ok {
				return nil, false
			}
			return s, true
		})
	case TypeFloat32:
		return pack32(values, dt, func(v any) ([]uint32, bool) {
			s, ok := v.([]float32)
			if !ok {
				return nil, false
			}
			out := make([]uint32, len(s))
			for i := range s {
				out[i] = math.Float32bits(s[i])
			}
			return out, true
		})
	case TypeInt64:
		return pack64(values, dt, func(v any) ([]uint64, bool) {
			s, ok := v.([]int64)
			if !ok {
				return nil, false
			}
			out := make([]uint64, len(s))
			for i := range s {
				out[i] = uint64(s[i])
			}
			return out, true
		})
	case TypeUint64:
		return pack64(values, dt, func(v any) ([]uint64, bool) {
			s, ok := v.([]uint64)
			if !ok {
				return nil, false
			}
			return s, true
		})
	case TypeFloat64:
		return pack64(values, dt, func(v any) ([]uint64, bool) {
			s, ok := v.([]float64)
			if !ok {
				return nil, false
			}
			out := make([]uint64, len(s))
			for i := range s {
				out[i] = math.Float64bits(s[i])
			}
			return out, true
		})
	case TypeString:
		s, ok := values.([]string)
		if !ok {
			return 0, nil, nil, typeErr(dt, values)
		}
		cp := append([]string(nil), s...)
		return len(cp), nil, cp, nil
	case TypeTimestamp:
		switch s := values.(type) {
		case []Timestamp:
			b := make([]byte, 0, len(s)*16)
			for _, ts := range s {
				b = appendTimestamp(b, ts)
			}
			return len(s), b, nil, nil
		case []time.Time:
			b := make([]byte, 0, len(s)*16)
			for _, t := range s {
				b = appendTimestamp(b, NewTimestamp(t))
			}
			return len(s), b, nil, nil
		default:
			return 0, nil, nil, typeErr(dt, values)
		}
	case TypeExtendedFloat:
		s, ok := values.([]Extended)
		if !ok {
			return 0, nil, nil, typeErr(dt, values)
		}
		b := make([]byte, 0, len(s)*extendedSize)
		for _, e := range s {
			b = append(b, encodeExtended(e)...)
		}
		return len(s), b, nil, nil
	case TypeComplex64:
		s, ok := values.([]complex64)
		if !ok {
			return 0, nil, nil, typeErr(dt, values)
		}
		b := make([]byte, len(s)*8)
		for i, c := range s {
			binary.LittleEndian.PutUint32(b[i*8:], math.Float32bits(real(c)))
			binary.LittleEndian.PutUint32(b[i*8+4:], math.Float32bits(imag(c)))
		}
		return len(s), b, nil, nil
	case TypeComplex128:
		s, ok := values.([]complex128)
		if !ok {
			return 0, nil, nil, typeErr(dt, values)
		}
		b := make([]byte, len(s)*16)
		for i, c := range s {
			binary.LittleEndian.PutUint64(b[i*16:], math.Float64bits(real(c)))
			binary.LittleEndian.PutUint64(b[i*16+8:], math.Float64bits(imag(c)))
		}
		return len(s), b, nil, nil
	case TypeVoid:
		return 0, nil, nil, fmt.Errorf("%w: void channels have no values", ErrType)
	default:
		return 0, nil, nil, fmt.Errorf("%w: %s", ErrUnsupported, dt)
	}
}

func bytesOfInt8(s []int8) []byte {
	b := make([]byte, len(s))
	for i, v := range s {
		b[i] = byte(v)
	}
	return b
}

func pack16(values any, signed bool, dt DataType) (int, []byte, []string, error) {
	var n int
	var words []uint16
	if signed {
		s, ok := values.([]int16)
		if !ok {
			return 0, nil, nil, typeErr(dt, values)
		}
		n = len(s)
		words = make([]uint16, n)
		for i := range s {
			words[i] = uint16(s[i])
		}
	} else {
		s, ok := values.([]uint16)
		if !ok {
			return 0, nil, nil, typeErr(dt, values)
		}
		n = len(s)
		words = s
	}
	b := make([]byte, n*2)
	for i, w := range words {
		binary.LittleEndian.PutUint16(b[i*2:], w)
	}
	return n, b, nil, nil
}

func pack32(values any, dt DataType, conv func(any) ([]uint32, bool)) (int, []byte, []string, error) {
	words, ok := conv(values)
	if !ok {
		return 0, nil, nil, typeErr(dt, values)
	}
	b := make([]byte, len(words)*4)
	for i, w := range words {
		binary.LittleEndian.PutUint32(b[i*4:], w)
	}
	return len(words), b, nil, nil
}

func pack64(values any, dt DataType, conv func(any) ([]uint64, bool)) (int, []byte, []string, error) {
	words, ok := conv(values)
	if !ok {
		return 0, nil, nil, typeErr(dt, values)
	}
	b := make([]byte, len(words)*8)
	for i, w := range words {
		binary.LittleEndian.PutUint64(b[i*8:], w)
	}
	return len(words), b, nil, nil
}

func appendTimestamp(b []byte, ts Timestamp) []byte {
	var tmp [16]byte
	binary.LittleEndian.PutUint64(tmp[0:8], ts.Fraction)
	binary.LittleEndian.PutUint64(tmp[8:16], uint64(ts.Seconds))
	return append(b, tmp[:]...)
}
