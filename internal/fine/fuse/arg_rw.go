package fuse

import (
	"bytes"
	"unsafe"

	"github.com/rfratto/viceroy/internal/fine"
)

// argReader allows popping individual FUSE arguments off of the data slice.
// Any method that fails will panic with errIncomplete, allowing for recovery.
type argReader struct {
	data []byte
	off  int
}

// String pops a string from the arg reader.
func (ar *argReader) String() string {
	// Find a nul byte from our remaining data.
	buf := ar.data[ar.off:]
	nul := bytes.IndexByte(buf, 0)
	if len(buf) == 0 || nul == -1 {
		panic(errIncomplete)
	}

	res := buf[:nul]
	ar.off += len(res) + 1 // Add one to consume NUL byte
	return string(res)
}

// Bytes pops n bytes from the arg reader.
func (ar *argReader) Bytes(n int) []byte {
	buf := ar.data[ar.off:]
	res := make([]byte, n)
	copied := copy(res, buf)
	if copied != n {
		panic(errIncomplete)
	}
	ar.off += copied
	return res
}

// Pointer pops a pointer from the arg reader. sz indicates the number of bytes
// to read.
func (ar *argReader) Pointer(sz uintptr) unsafe.Pointer {
	buf := ar.data[ar.off:]
	if len(buf) < int(sz) {
		panic(errIncomplete)
	}
	ar.off += int(sz)
	return unsafe.Pointer(&buf[0])
}

// argWriter allows queueing individual FUSE arguments onto a data slice. It
// can then be flushed to the FUSE device.
type argWriter struct {
	rawHeader rawOutHeader
	buf       buffer
}

// newArgWriter createes a new arg writer, initializing it with hdr.
func newArgWriter(hdr fine.ResponseHeader) *argWriter {
	var aw argWriter

	rawHeader := rawOutHeader{
		Len:    0, // We don't know the length yet, but we'll update it later
		Error:  int32(hdr.Error),
		Unique: hdr.RequestID,
	}
	*(*rawOutHeader)(aw.Pointer(unsafe.Sizeof(rawHeader))) = rawHeader
	aw.rawHeader = rawHeader

	return &aw
}

// String writes s as a NUL-terminated C string.
func (aw *argWriter) String(s string) {
	data := []byte(s)
	out := aw.buf.alloc(len(data) + 1) // +1 for the NUL byte
	n := copy(out, data)
	if n != len(data)+1 {
		panic(errIncomplete)
	}
}

// Bytes writes b.
func (aw *argWriter) Bytes(b []byte) {
	out := aw.buf.alloc(len(b))
	n := copy(out, b)
	if n != len(b) {
		panic(errIncomplete)
	}
}

// Pointer allocates sz bytes and returns the raw pointer. The caller should
// then set the mmeory of the pointer to the requested data. Do not retain the
// pointer after initially setting the memory.
func (aw *argWriter) Pointer(sz uintptr) unsafe.Pointer {
	return aw.buf.allocp(sz)
}

// Finish completes the argWriter, returning the final set of data. The final
// length of data will automatically be written into the header.
func (aw *argWriter) Finish() []byte {
	aw.rawHeader.Len = uint32(len(aw.buf))
	*(*rawOutHeader)(unsafe.Pointer(&aw.buf[0])) = aw.rawHeader
	return aw.buf
}

type buffer []byte

// alloc allocates n bytes and returns it as a byte slice. The resulting slice
// will be exactly n bytes long; do not append.
func (b *buffer) alloc(n int) []byte {
	// If there's not enough room for n more bytes, allocate a new slice and copy
	// over the old data. Existing unsafe.Pointers to the old slice will now be
	// invalid.
	if len(*b)+int(n) > cap(*b) {
		old := *b
		*b = make([]byte, len(*b), 2*cap(*b)+int(n))
		copy(*b, old)
	}
	// Grow the buffer
	off := len(*b)
	*b = (*b)[:off+int(n)]
	return (*b)[off:]
}

// allocp allocates n bytes and returns a pointer to the allocated data. The
// pointer must not be retained; further allocates may move the buffer.
func (b *buffer) allocp(n uintptr) unsafe.Pointer {
	slice := b.alloc(int(n))
	return unsafe.Pointer(&slice[0])
}
