package fuse

import "unsafe"

// align64 aligns numBytes to the next multiple of 64 bits.
func align64(numBytes uint64) uint64 {
	// The alignment works by creating a bit mask for bits which may appear in
	// any 64-bit aligned number. The mask is calculated through
	// NOT(sizeof(uint64) - 1) which is represened as:
	//
	// bits 63-32: 1111 1111 1111 1111 1111 1111 1111 1111
	// bits 31-0:  1111 1111 1111 1111 1111 1111 1111 1000
	//
	// i.e., numBytes is 64-bit aligned IFF it's a multiple of 8 bytes. None of
	// the lowest 3 bits will be set when it's a multiple of 8.
	//
	// Performing a bitwise AND with this mask will align it down towards the
	// previous multiple. However, we want to round to the next multiple, not
	// the previous. To work around this, we increase numBytes by 7; one less
	// than the next multiple when it's already aligned. This means that aligned
	// values will stay within the same alignment, while unaligned numbers will
	// enter the range of the next alignment.
	//
	// For example, 24 bytes is a multiple of 8 bytes and already aligned. All
	// values in the [24, 31] range can be considered to be part of the 24-byte
	// alignment. Adding 7 to 24 brings it to 31. Rounding down to the previous
	// multiple returns it to 24.
	//
	// However, 25 bytes is not a multiple of 8. Adding 7 will increase it to 32,
	// which is part of a new multiple range. We then return 32.
	const size64 = uint64(unsafe.Sizeof(uint64(0)))
	return (numBytes + size64 - 1) & ^(size64 - 1)
}
