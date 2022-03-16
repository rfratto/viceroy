package fuse

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/rfratto/viceroy/internal/fine"
	"go.uber.org/atomic"
)

var errIncomplete = fmt.Errorf("fuse: incomplete message")

// devTransport implements fine.Transport by communicating with a `/dev/fuse`
// device file.
type devTransport struct {
	log log.Logger

	f *os.File

	closed  atomic.Bool
	onClose func()

	rmut, wmut sync.Mutex
}

var _ fine.Transport = (*devTransport)(nil)

var maxRequestSize = syscall.Getpagesize()
var bufSize = maxRequestSize + int(MaxWrite)

// MaxWrite size supported. Linux 4.2.0 caps this value at 128kB.
var MaxWrite uint32 = 128 * 1024

func (kc *devTransport) RecvRequest() (hdr fine.RequestHeader, req fine.Request, err error) {
	// Our argument reader might throw a panic in invalid messages. We want to
	// catch it here and return an error instead.
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		if rerr, ok := r.(error); ok && errors.Is(rerr, errIncomplete) {
			err = rerr
			return
		}
		// Not from argReader, throw it back
		panic(r)
	}()

	buf := make([]byte, bufSize)
	kc.rmut.Lock()
	n, err := syscall.Read(int(kc.f.Fd()), buf)
	kc.rmut.Unlock()
	if n <= 0 {
		level.Debug(kc.log).Log("msg", "read no data from driver", "err", err)
		return hdr, nil, io.EOF
	}
	if err != nil {
		level.Error(kc.log).Log("msg", "failed to read from driver", "err", err)
		return hdr, nil, err
	}
	buf = buf[:n]

	ar := argReader{data: buf}

	var rawHdr rawInHeader
	rawHdr = *(*rawInHeader)(ar.Pointer(unsafe.Sizeof(rawHdr)))
	if rawHdr.Len != uint32(len(buf)) {
		return hdr, nil, fmt.Errorf("fuse: header length %d doesn't match message %d", rawHdr.Len, len(buf))
	}
	hdr = toRequestHeader(rawHdr)

	// Here be dragons, and they're unsafe. The following non-default switch
	// cases use the following pattern:
	//
	// A variable group is declared at the top of each case to describe the
	// additional arguments passed by fine. If there's no variable group, there's
	// no additional arguments. While many of the code could be simplified into a
	// single line, the style is chosen for consistency and making it easier to
	// reason about the correctness of the code as a while.
	//
	// Arguments are listed in the same order that FUSE sends them. Do not
	// re-order them, otherwise everything will catch on fire. Reading an
	// argument may panic if the available data isn't big enough. This panic will
	// be caught at the top of the function and returned to the user as an error.
	// This is used to greatly simplify the code, which otherwise would be a
	// mess.
	switch rawHdr.Opcode {
	default:
		// We purposefully don't implement a few opcodes, but we'd like to log a
		// warning if we come across an opcode we've never seen before.
		if _, known := unimplementedOps[rawHdr.Opcode]; !known {
			// Unkonwn upcode
			level.Warn(kc.log).Log("msg", "unknown opcode", "opcode", rawHdr.Opcode)
		}
		// Return an empty request body. The receiver must respond back to the
		// server with fine.ErrorUnimplemented, otherwise FUSE might hang.
		return hdr, nil, nil

	case fine.OpLookup:
		var (
			name = ar.String()
		)
		if name == "\u0003" {
			os.Exit(1)
		}
		return hdr, &fine.LookupRequest{Name: name}, nil

	case fine.OpForget:
		var (
			in = *(*rawForgetIn)(ar.Pointer(unsafe.Sizeof(rawForgetIn{})))
		)
		return hdr, &fine.ForgetRequest{NumLookups: in.NLookup}, nil

	case fine.OpGetattr:
		var (
			in = *(*rawGetattrIn)(ar.Pointer(unsafe.Sizeof(rawGetattrIn{})))
		)
		return hdr, &fine.GetattrRequest{
			Flags:  fine.GetAttribFlags(in.GetattrFlags),
			Handle: fine.Handle(in.Fh),
		}, nil

	case fine.OpSetattr:
		var (
			in = *(*rawSetattrIn)(ar.Pointer(unsafe.Sizeof(rawSetattrIn{})))
		)
		return hdr, &fine.SetattrRequest{
			UpdateMask: fine.AttribMask(in.Valid),
			Handle:     fine.Handle(in.Fh),
			Size:       in.Size,
			LockOwner:  fine.LockOwner(in.LockOwner),
			LastAccess: time.Unix(int64(in.Atime), int64(in.AtimeNsec)),
			LastModify: time.Unix(int64(in.Mtime), int64(in.MtimeNsec)),
			LastChange: time.Unix(int64(in.Ctime), int64(in.CtimeNsec)),
			Mode:       toNativeMode(in.Mode),
			UID:        in.UID,
			GID:        in.GID,
		}, nil

	case fine.OpReadlink:
		return hdr, nil, nil

	case fine.OpSymlink:
		var (
			source   = ar.String()
			linkname = ar.String()
		)
		return hdr, &fine.SymlinkRequest{Source: source, LinkName: linkname}, nil

	case fine.OpMknod:
		var (
			in   = *(*rawMknodIn)(ar.Pointer(unsafe.Sizeof(rawMknodIn{})))
			name = ar.String()
		)
		return hdr, &fine.MknodRequest{
			Mode:     toNativeMode(in.Mode),
			DeviceID: in.Rdev,
			Umask:    toNativeMode(in.Umask),
			Name:     name,
		}, nil

	case fine.OpMkdir:
		var (
			in   = *(*rawMkdirIn)(ar.Pointer(unsafe.Sizeof(rawMkdirIn{})))
			name = ar.String()
		)
		return hdr, &fine.MkdirRequest{
			Mode:  toNativeMode(in.Mode),
			Umask: toNativeMode(in.Umask),
			Name:  name,
		}, nil

	case fine.OpUnlink:
		var (
			name = ar.String()
		)
		return hdr, &fine.UnlinkRequest{Name: name}, nil

	case fine.OpRmdir:
		var (
			name = ar.String()
		)
		return hdr, &fine.RmdirRequest{Name: name}, nil

	case fine.OpRename:
		var (
			in      = *(*rawRenameIn)(ar.Pointer(unsafe.Sizeof(rawRenameIn{})))
			oldName = ar.String()
			newName = ar.String()
		)
		return hdr, &fine.RenameRequest{
			NewDir:  fine.Node(in.Newdir),
			OldName: oldName,
			NewName: newName,
		}, nil

	case fine.OpLink:
		var (
			in      = *(*rawLinkIn)(ar.Pointer(unsafe.Sizeof(rawLinkIn{})))
			newName = ar.String()
		)
		return hdr, &fine.LinkRequest{
			OldNode: fine.Node(in.OldNodeID),
			NewName: newName,
		}, nil

	case fine.OpOpen, fine.OpOpendir:
		var (
			in = *(*rawOpenIn)(ar.Pointer(unsafe.Sizeof(rawOpenIn{})))
		)
		return hdr, &fine.OpenRequest{
			Flags: fine.FileFlags(in.Flags),
		}, nil

	case fine.OpRead, fine.OpReaddir:
		var (
			in = *(*rawReadIn)(ar.Pointer(unsafe.Sizeof(rawReadIn{})))
		)
		return hdr, &fine.ReadRequest{
			Handle:    fine.Handle(in.Fh),
			Offset:    in.Offset,
			Size:      in.Size,
			Flags:     fine.ReadFlags(in.ReadFlags),
			LockOwner: fine.LockOwner(in.LockOwner),
			FileFlags: fine.FileFlags(in.Flags),
		}, nil

	case fine.OpWrite:
		var (
			in   = *(*rawWriteIn)(ar.Pointer(unsafe.Sizeof(rawWriteIn{})))
			data = ar.Bytes(int(in.Size))
		)
		return hdr, &fine.WriteRequest{
			Handle:    fine.Handle(in.Fh),
			Offset:    in.Offset,
			Flags:     fine.WriteFlags(in.WriteFlags),
			LockOwner: fine.LockOwner(in.LockOwner),
			FileFlags: fine.FileFlags(in.Flags),
			Data:      data,
		}, nil

	case fine.OpRelease, fine.OpReleasedir:
		var (
			in = *(*rawReleaseIn)(ar.Pointer(unsafe.Sizeof(rawReleaseIn{})))
		)
		return hdr, &fine.ReleaseRequest{
			Handle:    fine.Handle(in.Fh),
			Flags:     fine.ReleaseFlags(in.ReleaseFlags),
			FileFlags: fine.FileFlags(in.Flags),
			LockOwner: fine.LockOwner(in.LockOwner),
		}, nil

	case fine.OpFsync, fine.OpFsyncDir:
		var (
			in = *(*rawFsyncIn)(ar.Pointer(unsafe.Sizeof(rawFsyncIn{})))
		)
		return hdr, &fine.FsyncRequest{
			Handle: fine.Handle(in.Fh),
			Flags:  fine.SyncFlags(in.FsyncFlags),
		}, nil

	case fine.OpFlush:
		var (
			in = *(*rawFlushIn)(ar.Pointer(unsafe.Sizeof(rawFlushIn{})))
		)
		return hdr, &fine.FlushRequest{
			Handle:    fine.Handle(in.Fh),
			LockOwner: fine.LockOwner(in.LockOwner),
		}, nil

	case fine.OpInit:
		var (
			in = *(*rawInitIn)(ar.Pointer(unsafe.Sizeof(rawInitIn{})))
		)
		return hdr, &fine.InitRequest{
			LatestVersion: fine.Version{Major: in.Major, Minor: in.Minor},
			MaxReadahead:  in.MaxReadahead,
			Flags:         fine.InitFlags(in.Flags),
		}, nil

	case fine.OpAccess:
		var (
			in = *(*rawAccessIn)(ar.Pointer(unsafe.Sizeof(rawAccessIn{})))
		)
		return hdr, &fine.AccessRequest{
			Mask: toNativeMode(in.Mask),
		}, nil

	case fine.OpCreate:
		var (
			in   = *(*rawCreateIn)(ar.Pointer(unsafe.Sizeof(rawCreateIn{})))
			name = ar.String()
		)
		return hdr, &fine.CreateRequest{
			Flags: fine.FileFlags(in.Flags),
			Mode:  toNativeMode(in.Mode),
			Umask: toNativeMode(in.Umask),
			Name:  name,
		}, nil

	case fine.OpInterrupt:
		var (
			in = *(*rawInterruptIn)(ar.Pointer(unsafe.Sizeof(rawInterruptIn{})))
		)
		return hdr, &fine.InterruptRequest{RequestID: in.Unique}, nil

	case fine.OpDestroy:
		return hdr, nil, nil

	case fine.OpBatchForget:
		// BatchForget sends an array of arguments so we use the final type here
		// and the raw type in the loop.
		var (
			in    = *(*rawBatchForgetIn)(ar.Pointer(unsafe.Sizeof(rawBatchForgetIn{})))
			items []fine.BatchForgetItem
		)
		for i := 0; i < int(in.Count); i++ {
			item := *(*rawForgetOne)(ar.Pointer(unsafe.Sizeof(rawForgetOne{})))
			items = append(items, fine.BatchForgetItem{
				Node:       fine.Node(item.NodeID),
				NumLookups: item.Nlookup,
			})
		}
		return hdr, &fine.BatchForgetRequest{Items: items}, nil

	case fine.OpLseek:
		var (
			in = *(*rawLseekIn)(ar.Pointer(unsafe.Sizeof(rawLseekIn{})))
		)
		return hdr, &fine.LseekRequest{
			Handle: fine.Handle(in.Fh),
			Offset: in.Offset,
			Whence: in.Whence,
		}, nil
	}
}

var unimplementedOps = map[fine.Op]struct{}{
	fine.OpStatfs:        struct{}{},
	fine.OpSetxattr:      struct{}{},
	fine.OpGetxattr:      struct{}{},
	fine.OpListxattr:     struct{}{},
	fine.OpRemovexattr:   struct{}{},
	fine.OpGetLock:       struct{}{},
	fine.OpSetLock:       struct{}{},
	fine.OpSetLockWait:   struct{}{},
	fine.OpBmap:          struct{}{},
	fine.OpIoctl:         struct{}{},
	fine.OpPoll:          struct{}{},
	fine.OpNotifyReply:   struct{}{},
	fine.OpFallocate:     struct{}{},
	fine.OpReaddirplus:   struct{}{},
	fine.OpRename2:       struct{}{},
	fine.OpCopyFileRange: struct{}{},
	fine.OpSetupMapping:  struct{}{},
	fine.OpRemoveMapping: struct{}{},
	fine.OpCUSEInit:      struct{}{},
}

func (kc *devTransport) SendResponse(h fine.ResponseHeader, resp fine.Response) (err error) {
	// Our argWriter might throw a panic for invalid messages. We want to catch
	// it here and return an error instead.
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		if rerr, ok := r.(error); ok && errors.Is(rerr, errIncomplete) {
			err = rerr
			return
		}
		// Not from argWriter, throw it back
		panic(r)
	}()

	// Set up our writer. If it has an error, we have nothing to write and should
	// skip straight to the end.
	var ar = newArgWriter(h)
	if ar.rawHeader.Error != 0 || resp == nil {
		goto Flush
	}

	// Before continuing, read the comment on the switch statement for
	// RecvRequest, as much of the same concerns apply here.
	//
	// Each type declares a variable group holding the list of arguments
	// following the header to write to fine. Not all operations will have a
	// resp.
	//
	// Pointers returned by ar.Pointer should not be retained, as their
	// references may be invalidated after other arguments are written.
	switch resp := resp.(type) {
	case *fine.EntryResponse:
		var (
			out = toRawEntryOut(resp.Entry)
		)
		*(*rawEntryOut)(ar.Pointer(unsafe.Sizeof(out))) = out

	case *fine.AttrResponse:
		var (
			out = rawAttrOut{
				AttrValid:     toSecondFrag(resp.TTL),
				AttrValidNsec: toNanosecondFrag(resp.TTL),
				Attr:          toRawAttr(resp.Attrib),
			}
		)
		*(*rawAttrOut)(ar.Pointer(unsafe.Sizeof(out))) = out

	case *fine.ReadlinkResponse:
		var (
			out = resp.Contents
		)
		ar.Bytes(out)

	case *fine.OpenedResponse:
		var (
			out = rawOpenOut{
				Fh:        uint64(resp.Handle),
				OpenFlags: uint32(resp.OpenedFlags),
			}
		)
		*(*rawOpenOut)(ar.Pointer(unsafe.Sizeof(out))) = out

	case *fine.ReadResponse:
		var (
			data = resp.Data
		)
		ar.Bytes(data)

	case *fine.WriteResponse:
		var (
			out = rawWriteOut{Size: resp.Written}
		)
		*(*rawWriteOut)(ar.Pointer(unsafe.Sizeof(out))) = out

	case *fine.InitResponse:
		var (
			out = rawInitOut{
				Major:               resp.EarliestVersion.Major,
				Minor:               resp.EarliestVersion.Minor,
				MaxReadahead:        resp.MaxReadahead,
				Flags:               uint32(resp.Flags),
				MaxBackground:       resp.MaxBackground,
				CongestionThreshold: resp.CongestionThreshold,
				MaxWrite:            resp.MaxWrite,
				TimeGran:            resp.TimeGran,
				MaxPages:            resp.MaxPages,
				MapAlignment:        resp.MapAlignment,
			}
		)
		*(*rawInitOut)(ar.Pointer(unsafe.Sizeof(out))) = out

	case *fine.ReaddirResponse:
		// ReadDir is probably one of the more compilcated responses to convert.
		// Linux expects to receieve a list of (rawDirent, string) tuples for each
		// entry.
		//
		// We keep track of offsets of each entry. The offset is respective to
		// the start of the entries and NOT the entire message.
		//
		// Furthermore, the beginning of each tuple MUST be 64-bit aligned for
		// compatibility with 32-bit Linux distributions. We'll add padding to each
		// entry when its length isn't 64-bit aligned.
		var (
			ents = resp.Entries
		)

		var offset uint64
		for _, ent := range ents {
			// The name for a directory entry doesn't have a NUL byte.
			nameBytes := []byte(ent.Name)

			rawEnt := rawDirent{
				Ino:     ent.Inode,
				Offset:  offset, // This entry starts at the current offset.
				NameLen: uint32(len(nameBytes)),
				Type:    uint32(ent.Type),
			}

			*(*rawDirent)(ar.Pointer(unsafe.Sizeof(rawEnt))) = rawEnt
			ar.Bytes(nameBytes)

			// Check if we need to add padding.
			rawSize := uint64(unsafe.Sizeof(rawEnt)) + uint64(len(nameBytes))
			written := rawSize
			alignedSize := align64(rawSize)
			if alignedSize > rawSize {
				padding := alignedSize - rawSize
				ar.Bytes(make([]byte, padding))
				written += padding
			}

			// Increase offset by the total amount of data we just wrote, including
			// padding.
			offset += written
		}

	case *fine.CreateResponse:
		var (
			ent = toRawEntryOut(resp.Entry)
			out = rawOpenOut{
				Fh:        uint64(resp.Handle),
				OpenFlags: uint32(resp.OpenedFlags),
			}
		)
		*(*rawEntryOut)(ar.Pointer(unsafe.Sizeof(ent))) = ent
		*(*rawOpenOut)(ar.Pointer(unsafe.Sizeof(out))) = out

	case *fine.LseekResponse:
		var (
			out = rawLseekOut{Offset: resp.Offset}
		)
		*(*rawLseekOut)(ar.Pointer(unsafe.Sizeof(out))) = out

	default:
		return fmt.Errorf("unknown response type %T", resp)
	}

Flush:
	data := ar.Finish()

	if len(data) != int(ar.rawHeader.Len) {
		level.Warn(kc.log).Log("msg", "header length doesn't match message length", "header", ar.rawHeader.Len, "msg", len(data))
	}
	kc.wmut.Lock()
	n, err := syscall.Write(int(kc.f.Fd()), data)
	kc.wmut.Unlock()
	if err != nil || n != len(data) {
		level.Error(kc.log).Log("msg", "failed to write to driver", "len", n, "expect_len", len(data), "err", err)
	}
	if err == nil && n != len(data) {
		return fmt.Errorf("fuse: unexpected partial write")
	}
	return err
}

// Close closes the connection to the kernel. No more reads or writes can occur.
func (kc *devTransport) Close() (err error) {
	if kc.closed.CAS(false, true) {
		err = kc.f.Close()
		if kc.onClose != nil {
			kc.onClose()
		}
		level.Debug(kc.log).Log("msg", "closed devtransport", "err", err)
	}
	return err
}

// Raw FUSE types from Linux. These must match Linux's definitions verbatim,
// including padding fields, as their values are (unsafely) populated directly
// from a buf.
//
// `_` fields are used for padding, as structs must be 64-bit aligned.

type rawAttr struct {
	Inode     uint64
	Size      uint64
	Blocks    uint64
	Atime     uint64
	Mtime     uint64
	Ctime     uint64
	ATimeNsec uint32
	MTimeNsec uint32
	CTimeNsec uint32
	Mode      uint32
	Nlink     uint32
	UID       uint32
	GID       uint32
	RDev      uint32
	BlockSize uint32
	_         uint32
}

type rawEntryOut struct {
	NodeID         uint64
	Generation     uint64
	EntryValid     uint64
	AttrValid      uint64
	EntryValidNsec uint32
	AttrValidNsec  uint32
	Attr           rawAttr
}

type rawForgetIn struct {
	NLookup uint64
}

type rawForgetOne struct {
	NodeID  uint64
	Nlookup uint64
}

type rawBatchForgetIn struct {
	Count uint32
	_     uint32
}

type rawGetattrIn struct {
	GetattrFlags uint32
	_            uint32
	Fh           uint64
}

type rawAttrOut struct {
	AttrValid     uint64
	AttrValidNsec uint32
	_             uint32
	Attr          rawAttr
}

type rawMknodIn struct {
	Mode  uint32
	Rdev  uint32
	Umask uint32
	_     uint32
}

type rawMkdirIn struct {
	Mode  uint32
	Umask uint32
}

type rawRenameIn struct {
	Newdir uint64
}

type rawLinkIn struct {
	OldNodeID uint64
}

type rawSetattrIn struct {
	Valid     uint32
	_         uint32
	Fh        uint64
	Size      uint64
	LockOwner uint64
	Atime     uint64
	Mtime     uint64
	Ctime     uint64
	AtimeNsec uint32
	MtimeNsec uint32
	CtimeNsec uint32
	Mode      uint32
	_         uint32
	UID       uint32
	GID       uint32
	_         uint32
}

type rawOpenIn struct {
	Flags uint32
	_     uint32
}

type rawCreateIn struct {
	Flags   uint32
	Mode    uint32
	Umask   uint32
	Padding uint32
}

type rawOpenOut struct {
	Fh        uint64
	OpenFlags uint32
	_         uint32
}

type rawReleaseIn struct {
	Fh           uint64
	Flags        uint32
	ReleaseFlags uint32
	LockOwner    uint64
}

type rawFlushIn struct {
	Fh        uint64
	_         uint32
	_         uint32
	LockOwner uint64
}

type rawReadIn struct {
	Fh        uint64
	Offset    uint64
	Size      uint32
	ReadFlags uint32
	LockOwner uint64
	Flags     uint32
	Padding   uint32
}

type rawWriteIn struct {
	Fh         uint64
	Offset     uint64
	Size       uint32
	WriteFlags uint32
	LockOwner  uint64
	Flags      uint32
	Padding    uint32
}

type rawWriteOut struct {
	Size uint32
	_    uint32
}

type rawFsyncIn struct {
	Fh         uint64
	FsyncFlags uint32
	_          uint32
}

type rawAccessIn struct {
	Mask uint32
	_    uint32
}

type rawInitIn struct {
	Major        uint32
	Minor        uint32
	MaxReadahead uint32
	Flags        uint32
}

type rawInitOut struct {
	Major               uint32
	Minor               uint32
	MaxReadahead        uint32
	Flags               uint32
	MaxBackground       uint16
	CongestionThreshold uint16
	MaxWrite            uint32
	TimeGran            uint32
	MaxPages            uint16
	MapAlignment        uint16
	_                   [8]uint32
}

type rawInterruptIn struct {
	Unique uint64
}

type rawInHeader struct {
	Len    uint32
	Opcode fine.Op
	Unique uint64
	NodeID uint64
	UID    uint32
	GID    uint32
	PID    uint32
	_      uint32
}

type rawOutHeader struct {
	Len    uint32
	Error  int32
	Unique uint64
}

type rawDirent struct {
	Ino     uint64
	Offset  uint64 // Byte offset to this entry.
	NameLen uint32
	Type    uint32

	// Linux defines `char name[]` here, which has no size. We don't have an
	// equivalent that works, plus we don't need the field. Previously, we used
	// `_ struct{}` here as a placeholder, since `struct{}` has no size. However,
	// this still increased the soze of the struct to 32 bytes, for some unknown
	// reason.
}

type rawLseekIn struct {
	Fh     uint64
	Offset uint64
	Whence uint32
	_      uint32
}

type rawLseekOut struct {
	Offset uint64
}

// Convert types

func toSecondFrag(d time.Duration) uint64 {
	return uint64(d / time.Second)
}

func toNanosecondFrag(d time.Duration) uint32 {
	// Remove the seconds from the duration
	rem := d - d.Truncate(time.Second)
	return uint32(rem.Nanoseconds())
}

func toUnix(t time.Time) uint64 {
	if t.IsZero() {
		return 0
	}
	return uint64(t.Unix())
}

func toUnixNsOffset(t time.Time) uint32 {
	if t.IsZero() {
		return 0
	}
	return uint32(t.Nanosecond())
}

func toRequestHeader(hdr rawInHeader) fine.RequestHeader {
	return fine.RequestHeader{
		Op:        hdr.Opcode,
		RequestID: hdr.Unique,
		Node:      fine.Node(hdr.NodeID),
		UID:       hdr.UID,
		GID:       hdr.GID,
		PID:       hdr.PID,
	}
}

func toRawEntryOut(in fine.Entry) rawEntryOut {
	return rawEntryOut{
		NodeID:         uint64(in.Node),
		Generation:     in.Generation,
		EntryValid:     toSecondFrag(in.EntryTTL),
		AttrValid:      toSecondFrag(in.AttribTTL),
		EntryValidNsec: toNanosecondFrag(in.EntryTTL),
		AttrValidNsec:  toNanosecondFrag(in.AttribTTL),
		Attr:           toRawAttr(in.Attrib),
	}
}

func toRawAttr(in fine.Attrib) rawAttr {
	return rawAttr{
		Inode:     in.Inode,
		Size:      in.Size,
		Blocks:    in.Blocks,
		Atime:     toUnix(in.LastAccess),
		Mtime:     toUnix(in.LastModify),
		Ctime:     toUnix(in.LastChange),
		ATimeNsec: toUnixNsOffset(in.LastAccess),
		MTimeNsec: toUnixNsOffset(in.LastModify),
		CTimeNsec: toUnixNsOffset(in.LastChange),
		Mode:      toLinuxMode(in.Mode),
		Nlink:     in.HardLinks,
		UID:       in.UID,
		GID:       in.GID,
		RDev:      in.RDev,
		BlockSize: in.BlockSize,
	}
}

func toLinuxMode(in os.FileMode) uint32 {
	var out uint32
	out = uint32(in) & 0o777
	switch {
	case in&os.ModeType == 0:
		out |= syscall.S_IFREG
	case in&os.ModeDir != 0:
		out |= syscall.S_IFDIR
	case in&os.ModeDevice != 0 && in&os.ModeCharDevice != 0:
		out |= syscall.S_IFCHR
	case in&os.ModeDevice != 0:
		out |= syscall.S_IFBLK
	case in&os.ModeNamedPipe != 0:
		out |= syscall.S_IFIFO
	case in&os.ModeSymlink != 0:
		out |= syscall.S_IFLNK
	case in&os.ModeSocket != 0:
		out |= syscall.S_IFSOCK
	}
	if in&os.ModeSetuid != 0 {
		out |= syscall.S_ISUID
	}
	if in&os.ModeSetgid != 0 {
		out |= syscall.S_ISGID
	}
	if in&os.ModeSticky != 0 {
		out |= syscall.S_ISVTX
	}
	return out
}

func toNativeMode(in uint32) os.FileMode {
	out := os.FileMode(in & 0777)
	switch in & syscall.S_IFMT {
	case syscall.S_IFBLK:
		out |= os.ModeDevice
	case syscall.S_IFCHR:
		out |= os.ModeDevice | os.ModeCharDevice
	case syscall.S_IFDIR:
		out |= os.ModeDir
	case syscall.S_IFIFO:
		out |= os.ModeNamedPipe
	case syscall.S_IFLNK:
		out |= os.ModeSymlink
	case syscall.S_IFREG:
		// nothing to do
	case syscall.S_IFSOCK:
		out |= os.ModeSocket
	case 0:
		out |= os.ModeIrregular
	}
	if in&syscall.S_ISGID != 0 {
		out |= os.ModeSetgid
	}
	if in&syscall.S_ISUID != 0 {
		out |= os.ModeSetuid
	}
	if in&syscall.S_ISVTX != 0 {
		out |= os.ModeSticky
	}
	return out
}
