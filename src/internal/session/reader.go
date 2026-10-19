package session

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"os"
)

type stopReader struct {
	stop <-chan struct{}
	r    io.Reader
}

func (r stopReader) Read(b []byte) (int, error) {
	select {
	case <-r.stop:
		return 0, io.EOF
	default:
		return r.r.Read(b)
	}
}

// BoundedLine scans every byte, but retains at most cap bytes of a record.
// This is deliberately independent of bufio.Scanner's token-size limit.
func boundedLine(r *bufio.Reader, cap int) (data []byte, size int64, newline bool, err error) {
	for {
		part, e := r.ReadSlice('\n')
		size += int64(len(part))
		if len(data) < cap {
			n := min(len(part), cap-len(data))
			data = append(data, part[:n]...)
		}
		if e == bufio.ErrBufferFull {
			continue
		}
		return data, size, len(part) > 0 && part[len(part)-1] == '\n', e
	}
}
func encodeRef(r Ref) []byte {
	b := make([]byte, RecordSize)
	for i, n := range []int64{r.Offset, r.Size, r.Timestamp, r.Turn, r.Flags, r.Visual} {
		binary.LittleEndian.PutUint64(b[i*8:], uint64(n))
	}
	return b
}
func readRef(f *os.File, line int64) (Ref, error) {
	var b [RecordSize]byte
	_, e := f.ReadAt(b[:], (line-1)*RecordSize)
	if e != nil {
		return Ref{}, e
	}
	return Ref{int64(binary.LittleEndian.Uint64(b[0:])), int64(binary.LittleEndian.Uint64(b[8:])), int64(binary.LittleEndian.Uint64(b[16:])), int64(binary.LittleEndian.Uint64(b[24:])), int64(binary.LittleEndian.Uint64(b[32:])), int64(binary.LittleEndian.Uint64(b[40:]))}, nil
}
func readAt(f *os.File, off, size, cap int64) ([]byte, error) {
	n := min(size, cap)
	b := make([]byte, n)
	got, e := f.ReadAt(b, off)
	if e == io.EOF {
		e = nil
	}
	return bytes.TrimRight(b[:got], "\r\n"), e
}
