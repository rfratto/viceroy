// Package voverlay implements the viceroy overlay filesystem, which combines
// two fine.Handlers into a single filesystem. voverlay is a special type of
// overlay, unlike the standard overlay mount type supported by Linux. The
// rules are:
//
// 1. The upper filesystem will be preferred for files that exist on both the
//    upper and the lower filesystem.
//
// 2. The lower filesystem will be preferred for creating new files or
//    directories if the parent directory exists on both filesystems.
//
// 3. Otherwise, the matching filesystem will be used for reads and writes for
//    files that only exist on one of the two filesystems.
//
// voverlay has no concept of "copy up," and both the upper and lower
// filesystems must be writeable. Other than these rules, the overlay should
// mostly behave as expected: directories that exist on both filesystems are
// merged, etc.
//
// It is expected that the upper filesystem will be a viceroy worker, and the
// lower filesystem will be the host machine filesystem.
package voverlay

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/hashicorp/go-multierror"
	"github.com/rfratto/viceroy/internal/fine"
	"github.com/rfratto/viceroy/internal/fine/cache"
	"github.com/rfratto/viceroy/internal/fine/server"
	"go.uber.org/atomic"
)

// New creates a new voverlay filesystem that overlays an upper filesystem on
// top of a lower filesystem.
func New(l log.Logger, upper, lower server.Handler) server.Handler {
	root := newOverlayNode()
	root.SaveIndirection(upper, fine.RootNode)
	root.SaveIndirection(lower, fine.RootNode)

	return &overlay{
		log:   l,
		cache: cache.New(l, root),

		preferUpper: [...]server.Handler{upper, lower},
		preferLower: [...]server.Handler{lower, upper},
	}
}

type overlay struct {
	log log.Logger

	preferUpper [2]server.Handler // upper, lower
	preferLower [2]server.Handler // lower, upper

	// Sometimes we pass our own custom requests to the upper/lower filesystems.
	// Because of this, we need to use our own request IDs for everything so we
	// don't accidentally collide with a request ID we receive.
	reqID atomic.Uint64

	cache *cache.Cache
}

var (
	_ server.Handler = (*overlay)(nil)
)

// overlayContext is set by the overlay middleware handler.
type overlayContext struct {
	NodeInfo cache.NodeInfo
	Node     *overlayNode
}

func (o *overlay) Init(ctx context.Context) error {
	var errs *multierror.Error

	for i, h := range o.preferUpper {
		err := h.Init(ctx)
		if err != nil {
			level.Error(o.log).Log("msg", "error when initializing overlay fs", "index", i, "err", err)
			err := fmt.Errorf("error when closing overlay %d: %w", i, err)
			errs = multierror.Append(errs, err)
		}
	}

	return errs.ErrorOrNil()
}

func (o *overlay) Close() error {
	var errs *multierror.Error

	for i, h := range o.preferUpper {
		err := h.Close()
		if err != nil {
			level.Error(o.log).Log("msg", "error when closing overlay fs", "index", i, "err", err)
			err := fmt.Errorf("error when closing overlay %d: %w", i, err)
			errs = multierror.Append(errs, err)
		}
	}
	return errs.ErrorOrNil()
}

func (o *overlay) Lookup(ctx context.Context, hdr *fine.RequestHeader, req *fine.LookupRequest) (_ *fine.EntryResponse, err error) {
	onode := newOverlayNode()
	defer func() {
		if err != nil {
			o.cleanupOverlay(ctx, onode)
		}
	}()

	// We'll want to iterate over both nodes and keep track of indirection for
	// both, even though the upper node is preferred. If neither FS has the node,
	// then we'll return the error from the upper FS.
	var (
		ent  *fine.Entry
		errs *multierror.Error
	)

	overlayed, err := o.overlayedHeaders(o.preferUpper, hdr)
	if err != nil {
		return nil, err
	} else if len(overlayed) == 0 {
		return nil, fine.ErrorNotExist
	}
	for _, ovh := range overlayed {
		resp, err := ovh.Handler.Lookup(ctx, ovh.Header, req)
		if err != nil {
			errs = multierror.Append(errs, err)
			continue
		}

		onode.SaveIndirection(ovh.Handler, resp.Entry.Node)
		if ent == nil {
			ent = &resp.Entry
		}
	}
	if err := errs.ErrorOrNil(); err != nil && ent == nil {
		return nil, err
	}

	// At least one of our filesystems has the node. Let's create a new cache
	// entry.
	ni, err := o.cache.AddNode(hdr.Node, req.Name, onode)
	if err != nil {
		return nil, err
	}

	// Update our entry to refer to the indirect node
	ent.Node = ni.ID
	ent.Generation = ni.Generation
	return &fine.EntryResponse{Entry: *ent}, nil
}

// cleanupOverlay will tell underlying filesystems to free references to an overlay node.
func (o *overlay) cleanupOverlay(ctx context.Context, on *overlayNode) {
	on.EachIndirection(func(h server.Handler, n fine.Node) {
		forgetHeader := &fine.RequestHeader{
			Op:        fine.OpForget,
			RequestID: o.reqID.Inc(),
			Node:      n,
		}
		h.Forget(ctx, forgetHeader, &fine.ForgetRequest{NumLookups: 1})
	})
}

// cleanupOverlayHandle will tell underlying filesystems to free references to an overlay handl3.
func (o *overlay) cleanupOverlayHandle(ctx context.Context, oh *overlayHandle) {
	oh.EachIndirection(func(h server.Handler, hdl fine.Handle) {
		forgetHeader := &fine.RequestHeader{
			Op:        fine.OpRelease,
			RequestID: o.reqID.Inc(),
			Node:      1,
		}
		_ = h.Release(ctx, forgetHeader, &fine.ReleaseRequest{Handle: hdl})
	})
}

// overlayedHeaders returns a set of transformed headers for handlers where the
// node specified by hdr exists for the overlayed raw handler. Handlers will be
// iterated over in the specified order; change the order to prioritize a
// specific handler over another.
func (o *overlay) overlayedHeaders(handlers [2]server.Handler, hdr *fine.RequestHeader) ([]overlayedHeader, error) {
	res := make([]overlayedHeader, 0, len(handlers))
	for _, handler := range handlers {
		_, node, err := o.cache.GetNode(hdr.Node)
		if err != nil {
			return nil, err
		}
		onode := node.(*overlayNode)

		underlying, ok := onode.GetIndirection(handler)
		if !ok {
			// handler doesn't have an entry for this node, continue
			continue
		}

		indirectHeader := *hdr
		indirectHeader.RequestID = o.reqID.Inc()
		indirectHeader.Node = underlying
		res = append(res, overlayedHeader{
			Handler: handler,
			Header:  &indirectHeader,
		})
	}

	return res, nil
}

type overlayedHeader struct {
	Handler server.Handler      // Handler to handle the request
	Header  *fine.RequestHeader // Transformed header for Handler
}

func (o *overlay) Forget(ctx context.Context, hdr *fine.RequestHeader, req *fine.ForgetRequest) {
	overlayed, err := o.overlayedHeaders(o.preferUpper, hdr)
	if err != nil {
		return
	}
	for _, ovh := range overlayed {
		ovh.Handler.Forget(ctx, ovh.Header, req)
	}
	_ = o.cache.ReleaseNode(hdr.Node, req.NumLookups)
}

func (o *overlay) Getattr(ctx context.Context, hdr *fine.RequestHeader, req *fine.GetattrRequest) (*fine.AttrResponse, error) {
	var (
		forwardResp *fine.AttrResponse
		errs        *multierror.Error

		oh *overlayHandle // Only set when fine.GetAttribFlagHandle set
	)

	// Getattr might specify that we should be using handles instead.
	if req.Flags&fine.GetAttribFlagHandle != 0 {
		_, h, err := o.cache.GetHandle(req.Handle)
		if err != nil {
			// Abort early: the handle should exist at least in our local cache.
			return nil, err
		}
		oh = h.(*overlayHandle)
	}

	overlayed, err := o.overlayedHeaders(o.preferUpper, hdr)
	if err != nil {
		return nil, err
	} else if len(overlayed) == 0 {
		return nil, fine.ErrorNotExist
	}
	for _, ovh := range overlayed {
		sendReq := *req

		// Get the indirect handle if Getattr was done on a handle
		if oh != nil {
			realHandle, exist := oh.GetIndirection(ovh.Handler)
			if !exist {
				errs = multierror.Append(errs, fine.ErrorStale)
				continue
			}
			sendReq.Handle = realHandle
		}

		resp, err := ovh.Handler.Getattr(ctx, ovh.Header, &sendReq)
		if err != nil {
			errs = multierror.Append(errs, err)
			continue
		}
		forwardResp = resp
		break
	}
	if forwardResp == nil {
		return nil, errs.ErrorOrNil()
	}
	return forwardResp, nil
}

func (o *overlay) Setattr(ctx context.Context, hdr *fine.RequestHeader, req *fine.SetattrRequest) (*fine.AttrResponse, error) {
	var (
		forwardResp *fine.AttrResponse
		errs        *multierror.Error

		oh *overlayHandle // Only set when fine.AttribMaskFileHandle set
	)

	// Setattr might specify that we should be using handles instead.
	if req.UpdateMask&fine.AttribMaskFileHandle != 0 {
		_, h, err := o.cache.GetHandle(req.Handle)
		if err != nil {
			// Abort early: the handle should exist at least in our local cache.
			return nil, err
		}
		oh = h.(*overlayHandle)
	}

	overlayed, err := o.overlayedHeaders(o.preferUpper, hdr)
	if err != nil {
		return nil, err
	} else if len(overlayed) == 0 {
		return nil, fine.ErrorNotExist
	}
	for _, ovh := range overlayed {
		sendReq := *req

		// Get the indirect handle if Getattr was done on a handle
		if oh != nil {
			realHandle, exist := oh.GetIndirection(ovh.Handler)
			if !exist {
				errs = multierror.Append(errs, fine.ErrorStale)
				continue
			}
			sendReq.Handle = realHandle
		}

		resp, err := ovh.Handler.Setattr(ctx, ovh.Header, &sendReq)
		if err != nil {
			errs = multierror.Append(errs, err)
			continue
		}
		forwardResp = resp
		break
	}
	if forwardResp == nil {
		return nil, errs.ErrorOrNil()
	}
	return forwardResp, nil
}

func (o *overlay) Readlink(ctx context.Context, hdr *fine.RequestHeader) (*fine.ReadlinkResponse, error) {
	overlayed, err := o.overlayedHeaders(o.preferUpper, hdr)
	if err != nil {
		return nil, err
	} else if len(overlayed) == 0 {
		return nil, fine.ErrorNotExist
	}

	var errs *multierror.Error
	for _, ovh := range overlayed {
		resp, err := ovh.Handler.Readlink(ctx, ovh.Header)
		if err != nil {
			errs = multierror.Append(errs, err)
			continue
		}
		return resp, nil
	}
	return nil, errs.ErrorOrNil()
}

func (o *overlay) Symlink(ctx context.Context, hdr *fine.RequestHeader, req *fine.SymlinkRequest) (*fine.EntryResponse, error) {
	// TODO(rfratto): implement symlink. It's not clear if we need to care about
	// cross-device linking here.
	return nil, fine.ErrorUnimplemented
}

func (o *overlay) Mknod(ctx context.Context, hdr *fine.RequestHeader, req *fine.MknodRequest) (*fine.EntryResponse, error) {
	// TODO(rfratto): Implement.
	return nil, fine.ErrorUnimplemented
}

func (o *overlay) Mkdir(ctx context.Context, hdr *fine.RequestHeader, req *fine.MkdirRequest) (_ *fine.EntryResponse, err error) {
	onode := newOverlayNode()
	defer func() {
		if err != nil {
			o.cleanupOverlay(ctx, onode)
		}
	}()

	overlayed, err := o.overlayedHeaders(o.preferLower, hdr)
	if err != nil {
		return nil, err
	} else if len(overlayed) == 0 {
		return nil, fine.ErrorNotExist
	}

	// We don't want to fall back when creating directories, so instead of
	// iterating over both overlayed nodes, we'll only use the first.
	overlay := overlayed[0]

	resp, err := overlay.Handler.Mkdir(ctx, overlay.Header, req)
	if err != nil {
		return nil, err
	}
	onode.SaveIndirection(overlay.Handler, resp.Entry.Node)

	ni, err := o.cache.AddNode(hdr.Node, req.Name, onode)
	if err != nil {
		return nil, err
	}

	return &fine.EntryResponse{
		Entry: fine.Entry{
			Node:       ni.ID,
			Generation: ni.Generation,
			EntryTTL:   resp.Entry.EntryTTL,
			AttribTTL:  resp.Entry.AttribTTL,
			Attrib:     resp.Entry.Attrib,
		},
	}, nil
}

func (o *overlay) Unlink(ctx context.Context, hdr *fine.RequestHeader, req *fine.UnlinkRequest) error {
	overlayed, err := o.overlayedHeaders(o.preferUpper, hdr)
	if err != nil {
		return err
	} else if len(overlayed) == 0 {
		return fine.ErrorNotExist
	}

	var errs *multierror.Error
	for _, ovh := range overlayed {
		err := ovh.Handler.Unlink(ctx, ovh.Header, req)
		if err != nil {
			errs = multierror.Append(errs, err)
			continue
		}
		return nil
	}
	return errs.ErrorOrNil()
}

func (o *overlay) Rmdir(ctx context.Context, hdr *fine.RequestHeader, req *fine.RmdirRequest) error {
	overlayed, err := o.overlayedHeaders(o.preferUpper, hdr)
	if err != nil {
		return err
	} else if len(overlayed) == 0 {
		return fine.ErrorNotExist
	}
	var errs *multierror.Error
	for _, ovh := range overlayed {
		err := ovh.Handler.Rmdir(ctx, ovh.Header, req)
		if err != nil {
			errs = multierror.Append(errs, err)
			continue
		}
		return nil
	}
	return errs.ErrorOrNil()
}

func (o *overlay) Rename(ctx context.Context, hdr *fine.RequestHeader, req *fine.RenameRequest) error {
	// Get the new local entry for the new directory where the file is being
	// moved to.
	_, rawNewDir, err := o.cache.GetNode(req.NewDir)
	if err != nil {
		return err
	}
	newDir := rawNewDir.(*overlayNode)

	// We want to mirror the request if the file exists in both locations. For
	// each FS, we'll lookup the node first, and then perform the rename if the
	// lookup is successful. If the lookup succeeds, the rename must also
	// succeed.
	//
	// While it's technical possible to implement moving between filesystems,
	// we're going to disallow it for now.
	overlayed, err := o.overlayedHeaders(o.preferUpper, hdr)
	if err != nil {
		return err
	} else if len(overlayed) == 0 {
		return fine.ErrorNotExist
	}

	var (
		errs       *multierror.Error
		nodeExists bool
	)
	for _, ovh := range overlayed {
		// Check that the old node exists.
		oldExists, cleanOld, err := o.checkNodeExists(ctx, ovh.Handler, ovh.Header.Node, req.OldName)
		if err != nil {
			errs = multierror.Append(errs, err)
			continue
		} else if !oldExists {
			// It's fine if the old node doesn't exist on a single filesystem.
			// However, if it doesn't exist on both, then we want to return
			// fine.ErrorNotExist.
			continue
		}
		defer cleanOld()

		nodeExists = true

		newDir, newDirExist := newDir.GetIndirection(ovh.Handler)
		if !newDirExist {
			// If the new directory doesn't exist on the handler, it's a cross-device
			// move. Reject.
			errs = multierror.Append(errs, fine.ErrorBadCrossLink)
			continue
		}

		err = ovh.Handler.Rename(ctx, ovh.Header, &fine.RenameRequest{
			NewDir:  newDir,
			OldName: req.OldName,
			NewName: req.NewName,
		})
		if err != nil {
			errs = multierror.Append(errs, err)
			continue
		}
	}

	if !nodeExists {
		// Neither of our overlays found the file.
		return fine.ErrorNotExist
	} else if err := errs.ErrorOrNil(); err != nil {
		return err
	}
	return o.cache.RenameNode(hdr.Node, req)
}

// checkNodeExists looks to see if a node is aware of the named file in the
// directory of parent. parent must be the indirect ID for h and not from the
// local cache.
//
// clean should be called on success where exist == true to free allocated
// resources from the check.
func (o *overlay) checkNodeExists(ctx context.Context, h server.Handler, parent fine.Node, name string) (exist bool, clean func(), err error) {
	lookupResp, err := h.Lookup(ctx, &fine.RequestHeader{
		Op:        fine.OpLookup,
		RequestID: o.reqID.Inc(),
		Node:      parent,
	}, &fine.LookupRequest{Name: name})
	if errors.Is(err, fine.ErrorStale) || errors.Is(err, fine.ErrorNoDevice) {
		return false, nil, nil
	} else if err != nil {
		return false, nil, err
	}

	clean = func() {
		h.Forget(ctx, &fine.RequestHeader{
			Op:        fine.OpForget,
			RequestID: o.reqID.Inc(),
			Node:      lookupResp.Entry.Node,
		}, &fine.ForgetRequest{NumLookups: 1})
	}
	return true, clean, nil
}

func (o *overlay) Link(ctx context.Context, hdr *fine.RequestHeader, req *fine.LinkRequest) (_ *fine.EntryResponse, err error) {
	// Get the new local entry for the old node.
	_, rawOldNode, err := o.cache.GetNode(req.OldNode)
	if err != nil {
		return nil, err
	}
	oldNode := rawOldNode.(*overlayNode)

	onode := newOverlayNode()
	defer func() {
		if err != nil {
			o.cleanupOverlay(ctx, onode)
		}
	}()

	overlayed, err := o.overlayedHeaders(o.preferUpper, hdr)
	if err != nil {
		return nil, err
	} else if len(overlayed) == 0 {
		return nil, fine.ErrorNotExist
	}

	// Link is handled much like Rename. If a node exists on an FS, the request must succeed.
	var (
		errs       *multierror.Error
		ent        *fine.Entry
		nodeExists bool
	)
	for _, ovh := range overlayed {
		// The old directory must exist on the underlayed FS.
		oldNodeRef, oldNodeExists := oldNode.GetIndirection(ovh.Handler)
		if !oldNodeExists {
			// Cross-device move. Reject.
			errs = multierror.Append(errs, fine.ErrorBadCrossLink)
			continue
		}
		nodeExists = true

		resp, err := ovh.Handler.Link(ctx, ovh.Header, &fine.LinkRequest{
			OldNode: oldNodeRef,
			NewName: req.NewName,
		})
		if err != nil {
			errs = multierror.Append(errs, err)
			continue
		}
		onode.SaveIndirection(ovh.Handler, resp.Entry.Node)

		ent = &resp.Entry
	}

	if !nodeExists {
		// Neither of our overlays found the old directory.
		return nil, fine.ErrorNotExist
	} else if err := errs.ErrorOrNil(); err != nil {
		return nil, err
	}

	// At least one of our filesystems has the node. Let's create a new cache
	// entry.
	ni, err := o.cache.AddNode(hdr.Node, req.NewName, onode)
	if err != nil {
		return nil, err
	}

	// Update our entry to refer to the indirect node
	ent.Node = ni.ID
	ent.Generation = ni.Generation
	return &fine.EntryResponse{Entry: *ent}, nil
}

func (o *overlay) Open(ctx context.Context, hdr *fine.RequestHeader, req *fine.OpenRequest) (_ *fine.OpenedResponse, err error) {
	ohandle := newOverlayHandle()
	defer func() {
		if err != nil {
			o.cleanupOverlayHandle(ctx, ohandle)
		}
	}()

	// Return the first successful call to Open.
	var (
		errs *multierror.Error
	)
	overlayed, err := o.overlayedHeaders(o.preferUpper, hdr)
	if err != nil {
		return nil, err
	} else if len(overlayed) == 0 {
		return nil, fine.ErrorNotExist
	}
	for _, ovh := range overlayed {
		resp, err := ovh.Handler.Open(ctx, ovh.Header, req)
		if err != nil {
			errs = multierror.Append(errs, err)
			continue
		}

		// Save our handle to the cache.
		localHandle, err := o.cache.AddHandle(ohandle)
		if err != nil {
			return nil, err
		}
		ohandle.SaveIndirection(ovh.Handler, resp.Handle)
		return &fine.OpenedResponse{Handle: localHandle.ID}, nil
	}
	return nil, errs.ErrorOrNil()
}

func (o *overlay) Read(ctx context.Context, hdr *fine.RequestHeader, req *fine.ReadRequest) (*fine.ReadResponse, error) {
	// Return on the first successful call to Read.
	overlayed, err := o.overlayedHandles(o.preferUpper, hdr, req.Handle)
	if err != nil {
		return nil, err
	} else if len(overlayed) == 0 {
		return nil, fine.ErrorNotExist
	}

	var errs *multierror.Error
	for _, ovh := range overlayed {
		sendReq := *req
		sendReq.Handle = ovh.Handle
		resp, err := ovh.Handler.Read(ctx, ovh.Header, &sendReq)
		if err != nil {
			errs = multierror.Append(errs, err)
			continue
		}
		return resp, nil
	}
	return nil, errs.ErrorOrNil()
}

// overlayedHandles is like overlayedHeaders but also includes handle
// indirection.
func (o *overlay) overlayedHandles(handlers [2]server.Handler, hdr *fine.RequestHeader, local fine.Handle) ([]overlayedHandle, error) {
	// Get the local handle
	_, handle, err := o.cache.GetHandle(local)
	if err != nil {
		return nil, err
	}
	ohandle := handle.(*overlayHandle)

	headers, err := o.overlayedHeaders(handlers, hdr)
	if err != nil {
		return nil, err
	}

	res := make([]overlayedHandle, 0, len(handlers))
	for _, header := range headers {
		underlyingHandle, ok := ohandle.GetIndirection(header.Handler)
		if !ok {
			// handler doesn't have an entry for this handle, continue
			continue
		}
		res = append(res, overlayedHandle{
			Handler: header.Handler,
			Header:  header.Header,
			Handle:  underlyingHandle,
		})
	}
	return res, nil
}

type overlayedHandle struct {
	Handler server.Handler      // Handler to handle the request
	Header  *fine.RequestHeader // Transformed header for Handler
	Handle  fine.Handle         // Indirect handle ID
}

func (o *overlay) Write(ctx context.Context, hdr *fine.RequestHeader, req *fine.WriteRequest) (*fine.WriteResponse, error) {
	// Return on the first successful call to Write.
	overlayed, err := o.overlayedHandles(o.preferUpper, hdr, req.Handle)
	if err != nil {
		return nil, err
	} else if len(overlayed) == 0 {
		return nil, fine.ErrorNotExist
	}

	var errs *multierror.Error
	for _, ovh := range overlayed {
		sendReq := *req
		sendReq.Handle = ovh.Handle
		resp, err := ovh.Handler.Write(ctx, ovh.Header, &sendReq)
		if err != nil {
			errs = multierror.Append(errs, err)
			continue
		}
		return resp, nil
	}
	return nil, errs.ErrorOrNil()
}

func (o *overlay) Release(ctx context.Context, hdr *fine.RequestHeader, req *fine.ReleaseRequest) error {
	// Call on each underlay that has the handle.
	overlayed, err := o.overlayedHandles(o.preferUpper, hdr, req.Handle)
	if err != nil {
		return err
	} else if len(overlayed) == 0 {
		return fine.ErrorNotExist
	}

	var errs *multierror.Error
	for _, ovh := range overlayed {
		sendReq := *req
		sendReq.Handle = ovh.Handle
		err := ovh.Handler.Release(ctx, ovh.Header, &sendReq)
		if err != nil {
			errs = multierror.Append(errs, err)
		}
	}
	return errs.ErrorOrNil()
}

func (o *overlay) Fsync(ctx context.Context, hdr *fine.RequestHeader, req *fine.FsyncRequest) error {
	// Call on each underlay that has the handle.
	overlayed, err := o.overlayedHandles(o.preferUpper, hdr, req.Handle)
	if err != nil {
		return err
	} else if len(overlayed) == 0 {
		return fine.ErrorNotExist
	}

	var errs *multierror.Error
	for _, ovh := range overlayed {
		sendReq := *req
		sendReq.Handle = ovh.Handle
		err := ovh.Handler.Fsync(ctx, ovh.Header, &sendReq)
		if err != nil {
			errs = multierror.Append(errs, err)
		}
	}
	return errs.ErrorOrNil()
}

func (o *overlay) Flush(ctx context.Context, hdr *fine.RequestHeader, req *fine.FlushRequest) error {
	// Call on each underlay that has the handle.
	overlayed, err := o.overlayedHandles(o.preferUpper, hdr, req.Handle)
	if err != nil {
		return err
	} else if len(overlayed) == 0 {
		return fine.ErrorNotExist
	}

	var errs *multierror.Error
	for _, ovh := range overlayed {
		sendReq := *req
		sendReq.Handle = ovh.Handle
		err := ovh.Handler.Flush(ctx, ovh.Header, &sendReq)
		if err != nil {
			errs = multierror.Append(errs, err)
		}
	}
	return errs.ErrorOrNil()
}

func (o *overlay) Opendir(ctx context.Context, hdr *fine.RequestHeader, req *fine.OpenRequest) (_ *fine.OpenedResponse, err error) {
	// Opendir is similar to Open, but we want to open the directory on both
	// underlays (if it exists in both) so readdir will combine.
	ohandle := newOverlayHandle()
	defer func() {
		if err != nil {
			o.cleanupOverlayHandle(ctx, ohandle)
		}
	}()

	// Return the first successful call to Opendir.
	var (
		errs   *multierror.Error
		opened bool
	)
	overlayed, err := o.overlayedHeaders(o.preferUpper, hdr)
	if err != nil {
		return nil, err
	} else if len(overlayed) == 0 {
		return nil, fine.ErrorNotExist
	}
	for _, ovh := range overlayed {
		resp, err := ovh.Handler.Opendir(ctx, ovh.Header, req)
		if err != nil {
			errs = multierror.Append(errs, err)
			continue
		}
		opened = true

		// Save our handle to the cache.
		ohandle.SaveIndirection(ovh.Handler, resp.Handle)
	}
	if !opened {
		return nil, errs.ErrorOrNil()
	}

	localHandle, err := o.cache.AddHandle(ohandle)
	if err != nil {
		return nil, err
	}
	return &fine.OpenedResponse{Handle: localHandle.ID}, nil
}

func (o *overlay) Readdir(ctx context.Context, hdr *fine.RequestHeader, req *fine.ReadRequest) (*fine.ReaddirResponse, error) {
	// Readdir combines entries from all nodes where the entry exists.
	var entrySets [][]fine.DirEntry

	overlayed, err := o.overlayedHandles(o.preferUpper, hdr, req.Handle)
	if err != nil {
		return nil, err
	} else if len(overlayed) == 0 {
		return nil, fine.ErrorNotExist
	}

	var errs *multierror.Error
	for _, ovh := range overlayed {
		sendReq := *req
		sendReq.Handle = ovh.Handle
		resp, err := ovh.Handler.Readdir(ctx, ovh.Header, &sendReq)
		if err != nil {
			errs = multierror.Append(errs, err)
			continue
		}
		entrySets = append(entrySets, resp.Entries)
	}

	if err := errs.ErrorOrNil(); err != nil {
		return nil, err
	}
	return &fine.ReaddirResponse{
		Entries: mergeEntrySets(entrySets...),
	}, nil
}

// mergeEntrySets merges multiple sets of directory entries together. Entries
// in earlier sets take precedence.
func mergeEntrySets(sets ...[]fine.DirEntry) []fine.DirEntry {
	var (
		res    []fine.DirEntry
		lookup = map[string]struct{}{}
	)
	for _, set := range sets {
		for _, dirent := range set {
			// Ignore entries that were already added
			if _, added := lookup[dirent.Name]; added {
				continue
			}
			res = append(res, dirent)
			lookup[dirent.Name] = struct{}{}
		}
	}
	return res
}

func (o *overlay) Releasedir(ctx context.Context, hdr *fine.RequestHeader, req *fine.ReleaseRequest) error {
	// Call on each underlay that has the handle.
	overlayed, err := o.overlayedHandles(o.preferUpper, hdr, req.Handle)
	if err != nil {
		return err
	} else if len(overlayed) == 0 {
		return fine.ErrorNotExist
	}

	var errs *multierror.Error
	for _, ovh := range overlayed {
		sendReq := *req
		sendReq.Handle = ovh.Handle
		err := ovh.Handler.Releasedir(ctx, ovh.Header, &sendReq)
		if err != nil {
			errs = multierror.Append(errs, err)
		}
	}
	return errs.ErrorOrNil()
}

func (o *overlay) Fsyncdir(ctx context.Context, hdr *fine.RequestHeader, req *fine.FsyncRequest) error {
	// Call on each underlay that has the handle.
	overlayed, err := o.overlayedHandles(o.preferUpper, hdr, req.Handle)
	if err != nil {
		return err
	} else if len(overlayed) == 0 {
		return fine.ErrorNotExist
	}

	var errs *multierror.Error
	for _, ovh := range overlayed {
		sendReq := *req
		sendReq.Handle = ovh.Handle
		err := ovh.Handler.Fsyncdir(ctx, ovh.Header, &sendReq)
		if err != nil {
			errs = multierror.Append(errs, err)
		}
	}
	return errs.ErrorOrNil()
}

func (o *overlay) Access(ctx context.Context, hdr *fine.RequestHeader, req *fine.AccessRequest) error {
	// Call on each underlay that has the node. The access must succeed on both.
	overlayed, err := o.overlayedHeaders(o.preferUpper, hdr)
	if err != nil {
		return err
	} else if len(overlayed) == 0 {
		return fine.ErrorNotExist
	}

	var errs *multierror.Error
	for _, ovh := range overlayed {
		err := ovh.Handler.Access(ctx, ovh.Header, req)
		if err != nil {
			errs = multierror.Append(errs, err)
		}
	}
	return errs.ErrorOrNil()
}

func (o *overlay) Create(ctx context.Context, hdr *fine.RequestHeader, req *fine.CreateRequest) (_ *fine.CreateResponse, err error) {
	var (
		onode   = newOverlayNode()
		ohandle = newOverlayHandle()
	)
	defer func() {
		if err != nil {
			o.cleanupOverlayHandle(ctx, ohandle)
			o.cleanupOverlay(ctx, onode)
		}
	}()

	// Return on the first successful call to create.
	var (
		errs *multierror.Error
	)
	overlayed, err := o.overlayedHeaders(o.preferLower, hdr)
	if err != nil {
		return nil, err
	} else if len(overlayed) == 0 {
		return nil, fine.ErrorNotExist
	}
	for _, ovh := range overlayed {
		resp, err := ovh.Handler.Create(ctx, ovh.Header, req)
		if err != nil {
			errs = multierror.Append(errs, err)
			continue
		}
		// Save the indirection for both the handle and the node.
		onode.SaveIndirection(ovh.Handler, resp.Entry.Node)
		ohandle.SaveIndirection(ovh.Handler, resp.Handle)

		// Add both the handle and the node to the cache.
		localNode, err := o.cache.AddNode(hdr.Node, req.Name, onode)
		if err != nil {
			return nil, err
		}
		localHandle, err := o.cache.AddHandle(ohandle)
		if err != nil {
			return nil, err
		}

		return &fine.CreateResponse{
			Handle: localHandle.ID,
			Entry: fine.Entry{
				Node:       localNode.ID,
				Generation: localNode.Generation,
				EntryTTL:   resp.Entry.EntryTTL,
				AttribTTL:  resp.Entry.AttribTTL,
				Attrib:     resp.Entry.Attrib,
			},
		}, nil
	}
	return nil, errs.ErrorOrNil()
}

func (o *overlay) BatchForget(ctx context.Context, hdr *fine.RequestHeader, req *fine.BatchForgetRequest) error {
	var errs *multierror.Error

	handlerBatches := make(map[server.Handler][]fine.BatchForgetItem, 2)

	for _, item := range req.Items {
		_, node, err := o.cache.GetNode(item.Node)
		if err != nil {
			errs = multierror.Append(errs, err)
			continue
		}
		onode := node.(*overlayNode)

		onode.EachIndirection(func(handler server.Handler, indirect fine.Node) {
			handlerBatches[handler] = append(handlerBatches[handler], fine.BatchForgetItem{
				Node:       indirect,
				NumLookups: item.NumLookups,
			})
		})

		if err := o.cache.ReleaseNode(item.Node, item.NumLookups); err != nil {
			errs = multierror.Append(errs, err)
		}
	}

	// Flush the stored batches.
	for handler, batch := range handlerBatches {
		err := handler.BatchForget(ctx, &fine.RequestHeader{
			Op:        fine.OpBatchForget,
			RequestID: o.reqID.Inc(),
			Node:      0, // Batch forgets don't use a node
		}, &fine.BatchForgetRequest{
			Items: batch,
		})
		if err != nil {
			errs = multierror.Append(errs, err)
		}
	}

	if err := errs.ErrorOrNil(); err != nil {
		level.Error(o.log).Log("msg", "batchforget failed", "err", err)
	}
	return nil
}

func (o *overlay) Lseek(ctx context.Context, hdr *fine.RequestHeader, req *fine.LseekRequest) (*fine.LseekResponse, error) {
	// Return on the first successful call to Lseek.
	overlayed, err := o.overlayedHandles(o.preferUpper, hdr, req.Handle)
	if err != nil {
		return nil, err
	} else if len(overlayed) == 0 {
		return nil, fine.ErrorNotExist
	}

	var errs *multierror.Error
	for _, ovh := range overlayed {
		sendReq := *req
		sendReq.Handle = ovh.Handle
		resp, err := ovh.Handler.Lseek(ctx, ovh.Header, &sendReq)
		if err != nil {
			errs = multierror.Append(errs, err)
			continue
		}
		return resp, nil
	}
	return nil, errs.ErrorOrNil()
}

type overlayNode struct {
	mut      sync.RWMutex
	indirect map[server.Handler]fine.Node // filesystem -> ID from that filesystem
}

func newOverlayNode() *overlayNode {
	return &overlayNode{
		indirect: make(map[server.Handler]fine.Node, 2),
	}
}

// EachIndirection iterates over the saved indirections.
func (o *overlayNode) EachIndirection(f func(server.Handler, fine.Node)) {
	o.mut.RLock()
	defer o.mut.RUnlock()
	for k, v := range o.indirect {
		f(k, v)
	}
}

// GetIndirection returns the underlying node ID for a specific handler.
func (o *overlayNode) GetIndirection(h server.Handler) (id fine.Node, exist bool) {
	o.mut.RLock()
	defer o.mut.RUnlock()
	val, ok := o.indirect[h]
	return val, ok
}

// SaveIndirection saves the underlying node ID for this node as seen by one of
// the upper or lower filesystems.
func (o *overlayNode) SaveIndirection(h server.Handler, indirect fine.Node) {
	o.mut.Lock()
	defer o.mut.Unlock()
	o.indirect[h] = indirect
}

func (o *overlayNode) Close() error {
	// no-op
	return nil
}

type overlayHandle struct {
	mut      sync.RWMutex
	indirect map[server.Handler]fine.Handle // filesystem -> ID from that filesystem

}

func newOverlayHandle() *overlayHandle {
	return &overlayHandle{
		indirect: make(map[server.Handler]fine.Handle, 2),
	}
}

// EachIndirection iterates over the saved indirections.
func (o *overlayHandle) EachIndirection(f func(server.Handler, fine.Handle)) {
	o.mut.RLock()
	defer o.mut.RUnlock()
	for k, v := range o.indirect {
		f(k, v)
	}
}

// GetIndirection returns the underlying node ID for a specific handler.
func (o *overlayHandle) GetIndirection(h server.Handler) (id fine.Handle, exist bool) {
	o.mut.RLock()
	defer o.mut.RUnlock()
	val, ok := o.indirect[h]
	return val, ok
}

// SaveIndirection saves the underlying handle ID for this handle as seen by
// one of the upper or lower filesystems.
func (o *overlayHandle) SaveIndirection(h server.Handler, indirect fine.Handle) {
	o.mut.Lock()
	defer o.mut.Unlock()
	o.indirect[h] = indirect
}

func (o *overlayHandle) Close() error {
	// no-op
	return nil
}
