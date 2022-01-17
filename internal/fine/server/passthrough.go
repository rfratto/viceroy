package server

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/rfratto/viceroy/internal/fine"
	"github.com/rfratto/viceroy/internal/fine/cache"
)

// Passthrough creates a new Handler which passes through requests to the host
// filesystem. Requests are transformed relative to the provided root. Note
// that this isn't a chroot, and it's possible to read files in higher
// directives via symbolic links.
func Passthrough(l log.Logger, root string) Handler {
	if l == nil {
		l = log.NewNopLogger()
	}
	return &passthroughHandler{
		log:   l,
		root:  root,
		cache: cache.New(l, &passthroughNode{inode: 1}),
	}
}

type passthroughHandler struct {
	log  log.Logger
	root string

	cache *cache.Cache

	mut sync.Mutex
}

var (
	_ Handler = (*passthroughHandler)(nil)
)

func (h *passthroughHandler) Init(ctx context.Context) error {
	// no-op
	return nil
}

var passthroughCacheKey struct{}

type passthroughContext struct {
	NodeInfo cache.NodeInfo
	Node     *passthroughNode
	NodePath string
}

func (h *passthroughHandler) getPassthroughContext(hdr *fine.RequestHeader) (*passthroughContext, error) {
	nodeInfo, node, err := h.cache.GetNode(hdr.Node)
	if err != nil {
		return nil, err
	}
	pnode, _ := node.(*passthroughNode)
	path, err := h.cache.NodePath(hdr.Node)
	if err != nil {
		return nil, err
	}
	return &passthroughContext{
		NodeInfo: nodeInfo,
		Node:     pnode,
		NodePath: path,
	}, nil
}

func (h *passthroughHandler) Close() error {
	h.mut.Lock()
	defer h.mut.Unlock()

	// TODO(rfratto): close open files (or cache?)
	return nil
}

func (h *passthroughHandler) Lookup(ctx context.Context, hdr *fine.RequestHeader, req *fine.LookupRequest) (*fine.EntryResponse, error) {
	return h.createNodeEntry(hdr.Node, req.Name)
}

// createNodeEntry gets the the stats of an existing file and caches it,
// returning an entry.
func (h *passthroughHandler) createNodeEntry(parent fine.Node, name string) (*fine.EntryResponse, error) {
	path, err := h.cache.NodePath(parent)
	if err != nil {
		return nil, err
	}
	fullPath := filepath.Join(h.root, path, name)
	fi, err := os.Lstat(fullPath)
	if err != nil {
		return nil, err
	}

	newNode := newPassthroughNode(parent, name)
	nodeInfo, err := h.cache.AddNode(parent, name, newNode)
	if err != nil {
		return nil, err
	}
	return &fine.EntryResponse{
		Entry: entryForNode(nodeInfo, newNode, fi),
	}, nil
}

func (h *passthroughHandler) Forget(ctx context.Context, hdr *fine.RequestHeader, req *fine.ForgetRequest) {
	err := h.cache.ReleaseNode(hdr.Node, req.NumLookups)
	if err != nil {
		level.Warn(h.log).Log("msg", "failed to forget node", "err", err)
	}
}

func (h *passthroughHandler) Getattr(ctx context.Context, hdr *fine.RequestHeader, req *fine.GetattrRequest) (*fine.AttrResponse, error) {
	pc, err := h.getPassthroughContext(hdr)
	if err != nil {
		return nil, err
	}

	var fi os.FileInfo
	switch {
	case req.Flags&fine.GetAttribFlagHandle != 0: // Get file info from handle
		var rawHandle cache.Handle
		_, rawHandle, err = h.cache.GetHandle(req.Handle)
		if err != nil {
			return nil, err
		}
		fi, err = rawHandle.(*passthroughHandle).f.Stat()
	default:
		fi, err = os.Lstat(filepath.Join(h.root, pc.NodePath))
	}
	if err != nil {
		return nil, err
	}

	return &fine.AttrResponse{
		TTL:    time.Minute,
		Attrib: attrFromInfo(pc.Node, fi),
	}, nil
}

func (h *passthroughHandler) Setattr(ctx context.Context, hdr *fine.RequestHeader, req *fine.SetattrRequest) (*fine.AttrResponse, error) {
	pc, err := h.getPassthroughContext(hdr)
	if err != nil {
		return nil, err
	}

	var f *os.File // File to update
	switch {
	case req.UpdateMask&fine.AttribMaskFileHandle != 0:
		_, handle, err := h.cache.GetHandle(req.Handle)
		if err != nil {
			return nil, err
		}
		f = handle.(*passthroughHandle).f
	default:
		// Let's open a temporary file for the request.
		f, err = os.OpenFile(filepath.Join(h.root, pc.NodePath), int(fine.OpenReadWrite), 0440)
		if err != nil {
			return nil, err
		}
		defer f.Close()
	}

	// NOTE(rfratto): Setattr isn't fully implemented thanks to our
	// platform-independent implementation lacking features that might be set as
	// flags here. We'll at least handle any flags we're capable of handling.
	if req.UpdateMask&fine.AttribMaskSize != 0 {
		if err := f.Truncate(int64(req.Size)); err != nil {
			return nil, err
		}
	}
	if req.UpdateMask&fine.AttribMaskMode != 0 {
		if err := f.Chmod(req.Mode); err != nil {
			return nil, err
		}
	}

	// Get the new file stats
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return &fine.AttrResponse{
		TTL:    time.Minute,
		Attrib: attrFromInfo(pc.Node, fi),
	}, nil
}

func (h *passthroughHandler) Readlink(ctx context.Context, hdr *fine.RequestHeader) (*fine.ReadlinkResponse, error) {
	pc, err := h.getPassthroughContext(hdr)
	if err != nil {
		return nil, err
	}
	res, err := os.Readlink(filepath.Join(h.root, pc.NodePath))
	if err != nil {
		return nil, err
	}
	return &fine.ReadlinkResponse{Contents: []byte(res)}, nil
}

func (h *passthroughHandler) Symlink(ctx context.Context, hdr *fine.RequestHeader, req *fine.SymlinkRequest) (*fine.EntryResponse, error) {
	pc, err := h.getPassthroughContext(hdr)
	if err != nil {
		return nil, err
	}
	var (
		oldname = filepath.Join(h.root, req.LinkName)
		newname = filepath.Join(h.root, pc.NodePath, req.Source)
	)
	if err := os.Symlink(oldname, newname); err != nil {
		return nil, err
	}
	return h.createNodeEntry(hdr.Node, req.Source)
}

func (h *passthroughHandler) Mknod(ctx context.Context, hdr *fine.RequestHeader, req *fine.MknodRequest) (*fine.EntryResponse, error) {
	return nil, fine.ErrorUnimplemented
}

func (h *passthroughHandler) Mkdir(ctx context.Context, hdr *fine.RequestHeader, req *fine.MkdirRequest) (*fine.EntryResponse, error) {
	pc, err := h.getPassthroughContext(hdr)
	if err != nil {
		return nil, err
	}
	var (
		newPath = filepath.Join(h.root, pc.NodePath, req.Name)
	)
	if err := os.Mkdir(newPath, req.Mode); err != nil {
		return nil, err
	}
	return h.createNodeEntry(hdr.Node, req.Name)
}

func (h *passthroughHandler) Unlink(ctx context.Context, hdr *fine.RequestHeader, req *fine.UnlinkRequest) error {
	pc, err := h.getPassthroughContext(hdr)
	if err != nil {
		return err
	}
	var (
		fullPath = filepath.Join(h.root, pc.NodePath, req.Name)
	)
	return os.Remove(fullPath)
}

func (h *passthroughHandler) Rmdir(ctx context.Context, hdr *fine.RequestHeader, req *fine.RmdirRequest) error {
	pc, err := h.getPassthroughContext(hdr)
	if err != nil {
		return err
	}
	var (
		fullPath = filepath.Join(h.root, pc.NodePath, req.Name)
	)
	return os.Remove(fullPath)
}

func (h *passthroughHandler) Rename(ctx context.Context, hdr *fine.RequestHeader, req *fine.RenameRequest) error {
	pc, err := h.getPassthroughContext(hdr)
	if err != nil {
		return err
	}
	var (
		oldPath = filepath.Join(h.root, pc.NodePath, req.OldName)
		newPath = filepath.Join(h.root, pc.NodePath, req.NewName)
	)
	if req.NewDir != hdr.Node {
		// It's being moved to a new directory. Get the path for the new directory.
		newDirPath, err := h.cache.NodePath(req.NewDir)
		if err != nil {
			return err
		}
		newPath = filepath.Join(h.root, newDirPath, req.NewName)
	}

	if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}

	// Attempt to move the entry in the cache, if it exists.
	_ = h.cache.RenameNode(hdr.Node, req)
	return nil
}

func (h *passthroughHandler) Link(ctx context.Context, hdr *fine.RequestHeader, req *fine.LinkRequest) (*fine.EntryResponse, error) {
	pc, err := h.getPassthroughContext(hdr)
	if err != nil {
		return nil, err
	}

	oldPath, err := h.cache.NodePath(req.OldNode)
	if err != nil {
		return nil, err
	}
	var (
		oldname = filepath.Join(h.root, oldPath)
		newname = filepath.Join(h.root, pc.NodePath, req.NewName)
	)
	if err := os.Link(oldname, newname); err != nil {
		return nil, err
	}
	return h.createNodeEntry(hdr.Node, req.NewName)
}

func (h *passthroughHandler) Open(ctx context.Context, hdr *fine.RequestHeader, req *fine.OpenRequest) (*fine.OpenedResponse, error) {
	pc, err := h.getPassthroughContext(hdr)
	if err != nil {
		return nil, err
	}
	var (
		path = filepath.Join(h.root, pc.NodePath)
	)
	f, err := os.OpenFile(path, int(req.Flags), 0)
	if err != nil {
		return nil, err
	}

	hi, err := h.cache.AddHandle(newPassthroughHandle(f, req.Flags))
	if err != nil {
		return nil, err
	}
	return &fine.OpenedResponse{Handle: hi.ID}, nil
}

func (h *passthroughHandler) Read(ctx context.Context, hdr *fine.RequestHeader, req *fine.ReadRequest) (*fine.ReadResponse, error) {
	_, hdl, err := h.cache.GetHandle(req.Handle)
	if err != nil {
		return nil, err
	}
	ph := hdl.(*passthroughHandle)

	buf := make([]byte, int(req.Size))
	n, err := ph.f.ReadAt(buf, int64(req.Offset))
	if errors.Is(err, io.EOF) {
		// NOTE(rfratto): We trim out io.EOF errors because Linux doesn't expect
		// io.EOF at the end of a file.
		err = nil
	}
	return &fine.ReadResponse{Data: buf[:n]}, err
}

func (h *passthroughHandler) Write(ctx context.Context, hdr *fine.RequestHeader, req *fine.WriteRequest) (*fine.WriteResponse, error) {
	_, hdl, err := h.cache.GetHandle(req.Handle)
	if err != nil {
		return nil, err
	}
	ph := hdl.(*passthroughHandle)

	var n int
	if ph.flags&fine.OpenAppend != 0 {
		// NOTE(rfratto): WriteAt fails if our file was opened for appending, so we
		// call Write here instead.
		n, err = ph.f.Write(req.Data)
	} else {
		n, err = ph.f.WriteAt(req.Data, int64(req.Offset))
	}
	if err != nil {
		return nil, err
	}
	return &fine.WriteResponse{Written: uint32(n)}, nil
}

func (h *passthroughHandler) Release(ctx context.Context, hdr *fine.RequestHeader, req *fine.ReleaseRequest) error {
	return h.cache.ReleaseHandle(req.Handle)
}

func (h *passthroughHandler) Fsync(ctx context.Context, hdr *fine.RequestHeader, req *fine.FsyncRequest) error {
	_, hdl, err := h.cache.GetHandle(req.Handle)
	if err != nil {
		return err
	}
	ph := hdl.(*passthroughHandle)
	return ph.f.Sync()
}

func (h *passthroughHandler) Flush(ctx context.Context, hdr *fine.RequestHeader, req *fine.FlushRequest) error {
	// no-op: flush may be called multiple times for a file, and we have nothing
	// to flush for our implementation of passthrough. We only care about
	// Forgets.
	return nil
}

func (h *passthroughHandler) Opendir(ctx context.Context, hdr *fine.RequestHeader, req *fine.OpenRequest) (*fine.OpenedResponse, error) {
	// Opendir works the same as Open, so we fall back to that.
	return h.Open(ctx, hdr, req)
}

func (h *passthroughHandler) Readdir(ctx context.Context, hdr *fine.RequestHeader, req *fine.ReadRequest) (*fine.ReaddirResponse, error) {
	_, hdl, err := h.cache.GetHandle(req.Handle)
	if err != nil {
		return nil, err
	}
	ph := hdl.(*passthroughHandle)

	ents, err := ph.f.ReadDir(0)
	if err != nil {
		return nil, err
	}
	fineEnt := make([]fine.DirEntry, len(ents))
	for i, ent := range ents {
		fineEnt[i] = fine.DirEntry{
			Inode: inodeHash(uint64(hdr.Node), ent.Name()),
			Type:  toFineEntry(ent.Type()),
			Name:  ent.Name(),
		}
	}
	return &fine.ReaddirResponse{Entries: fineEnt}, nil
}

func (h *passthroughHandler) Releasedir(ctx context.Context, hdr *fine.RequestHeader, req *fine.ReleaseRequest) error {
	return h.cache.ReleaseHandle(req.Handle)
}

func (h *passthroughHandler) Fsyncdir(ctx context.Context, hdr *fine.RequestHeader, req *fine.FsyncRequest) error {
	// Fsyncdir is equivalent to Fsync, so we fall back to that.
	return h.Fsync(ctx, hdr, req)
}

func (h *passthroughHandler) Access(ctx context.Context, hdr *fine.RequestHeader, req *fine.AccessRequest) error {
	return fine.ErrorUnimplemented
}

func (h *passthroughHandler) Create(ctx context.Context, hdr *fine.RequestHeader, req *fine.CreateRequest) (_ *fine.CreateResponse, err error) {
	pc, err := h.getPassthroughContext(hdr)
	if err != nil {
		return nil, err
	}

	// If anything during the Create fails, we want to undo anything saved
	// (closing the file, removing the cache entry, etc).
	var (
		newpath = filepath.Join(h.root, pc.NodePath, req.Name)
	)

	f, err := os.OpenFile(newpath, int(req.Flags)|os.O_CREATE, req.Mode)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = f.Close()
		}
	}()

	newEntry, err := h.createNodeEntry(hdr.Node, req.Name)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = h.cache.ReleaseNode(newEntry.Entry.Node, 1)
		}
	}()

	hi, err := h.cache.AddHandle(newPassthroughHandle(f, req.Flags))
	if err != nil {
		return nil, err
	}
	return &fine.CreateResponse{
		Handle: hi.ID,
		Entry:  newEntry.Entry,
	}, nil
}

func (h *passthroughHandler) BatchForget(ctx context.Context, hdr *fine.RequestHeader, req *fine.BatchForgetRequest) error {
	for _, item := range req.Items {
		err := h.cache.ReleaseNode(item.Node, item.NumLookups)
		if err != nil {
			level.Warn(h.log).Log("msg", "failed to forget node", "node", item.Node, "err", err)
		}
	}
	return nil
}

func (h *passthroughHandler) Lseek(ctx context.Context, hdr *fine.RequestHeader, req *fine.LseekRequest) (*fine.LseekResponse, error) {
	_, hdl, err := h.cache.GetHandle(req.Handle)
	if err != nil {
		return nil, err
	}
	ph := hdl.(*passthroughHandle)

	off, err := ph.f.Seek(int64(req.Offset), int(req.Whence))
	if err != nil {
		return nil, err
	}
	return &fine.LseekResponse{Offset: uint64(off)}, nil
}

type passthroughNode struct {
	inode uint64
}

func newPassthroughNode(parent fine.Node, name string) *passthroughNode {
	return &passthroughNode{
		inode: inodeHash(uint64(parent), name),
	}
}

func (n *passthroughNode) Close() error {
	// no-op
	return nil
}

// inodeHash returns a fake inode number given the hash of the file name and
// the parent directory's inode number.
func inodeHash(parent uint64, name string) uint64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%02b%s", parent, name)

	var inode uint64
	for {
		inode = h.Sum64()
		if inode != 1 {
			break
		}
		// inode 1 is reserved for the root; try something else.
		h.Write([]byte{'!'})
	}
	return inode
}

type passthroughHandle struct {
	f     *os.File
	flags fine.FileFlags
}

func newPassthroughHandle(f *os.File, flags fine.FileFlags) *passthroughHandle {
	return &passthroughHandle{f: f, flags: flags}
}

func (h *passthroughHandle) Close() error { return h.f.Close() }

func entryForNode(ni cache.NodeInfo, n *passthroughNode, fi fs.FileInfo) fine.Entry {
	return fine.Entry{
		Node:       ni.ID,
		Generation: ni.Generation,
		EntryTTL:   time.Minute,
		AttribTTL:  time.Minute,
		Attrib:     attrFromInfo(n, fi),
	}
}

func toFineEntry(m os.FileMode) (et fine.EntryType) {
	// TODO(rfratto): we should probably just have fine use the real entry type and
	// convert to the raw entry just for the fuse transport.
	switch {
	case m&os.ModeNamedPipe != 0:
		et |= fine.EntryPipe
	case m&os.ModeCharDevice != 0 && m&os.ModeDevice != 0:
		et |= fine.EntryCharacter
	case m&os.ModeDir != 0:
		et |= fine.EntryDirectory
	case m&os.ModeDevice != 0:
		et |= fine.EntryBlock
	case m&os.ModeType == 0:
		et |= fine.EntryRegular
	case m&os.ModeSymlink != 0:
		et |= fine.EntryLink
	case m&os.ModeSocket != 0:
		et |= fine.EntryUnixSocket
	}
	return
}
