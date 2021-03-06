package server

import (
	"context"
	"fmt"
	"sync"

	"github.com/rfratto/viceroy/internal/fine"
)

// LazyHandler is a Handler which allows to defer setting of the real Handler
// implementation. The zero value is ready for use.
type LazyHandler struct {
	mut         sync.RWMutex
	inner       Handler
	initialized bool
	closed      bool
}

var (
	_ Handler = (*LazyHandler)(nil)
)

// SetHandler configures LazyHandler to forward requests to the specified h.
// SetHandler may not be called after LazyHandler has been closed.
//
// h.Init will immediately be called if the lazy handler has already been
// initialized.
func (lh *LazyHandler) SetHandler(ctx context.Context, h Handler) error {
	lh.mut.Lock()
	defer lh.mut.Unlock()

	if lh.closed {
		return fmt.Errorf("LazyHandler closed")
	}

	lh.inner = h
	if lh.initialized && lh.inner != nil {
		// We were previously initialized. Immediately initialize h.
		return lh.inner.Init(ctx)
	}
	return nil
}

// Init implements Handler. Init will forward the Init to the inner Handler
// whenever it is set.
func (lh *LazyHandler) Init(ctx context.Context) error {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	lh.initialized = true

	if lh.inner != nil {
		// We already have an inner handler; we can call its init immediately.
		return lh.inner.Init(ctx)
	}
	return nil
}

// Close closes the LazyHandler and the inner handler, if set. The returned
// error will be propagated from the inner handler.
func (lh *LazyHandler) Close() error {
	lh.mut.Lock()
	defer lh.mut.Unlock()

	lh.closed = true

	var err error
	if lh.inner != nil {
		err = lh.inner.Close()
	}
	lh.inner = nil
	return err
}

func (lh *LazyHandler) Lookup(ctx context.Context, h *fine.RequestHeader, req *fine.LookupRequest) (*fine.EntryResponse, error) {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return nil, fine.ErrorIO
	case lh.inner == nil:
		return nil, fine.ErrorNotExist
	default:
		return lh.inner.Lookup(ctx, h, req)
	}
}

func (lh *LazyHandler) Forget(ctx context.Context, h *fine.RequestHeader, req *fine.ForgetRequest) {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		// no-op
	case lh.inner == nil:
		// no-op
	default:
		lh.inner.Forget(ctx, h, req)
	}
}

func (lh *LazyHandler) Getattr(ctx context.Context, h *fine.RequestHeader, req *fine.GetattrRequest) (*fine.AttrResponse, error) {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return nil, fine.ErrorIO
	case lh.inner == nil:
		return nil, fine.ErrorNotExist
	default:
		return lh.inner.Getattr(ctx, h, req)
	}
}

func (lh *LazyHandler) Setattr(ctx context.Context, h *fine.RequestHeader, req *fine.SetattrRequest) (*fine.AttrResponse, error) {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return nil, fine.ErrorIO
	case lh.inner == nil:
		return nil, fine.ErrorNotExist
	default:
		return lh.inner.Setattr(ctx, h, req)
	}
}

func (lh *LazyHandler) Readlink(ctx context.Context, h *fine.RequestHeader) (*fine.ReadlinkResponse, error) {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return nil, fine.ErrorIO
	case lh.inner == nil:
		return nil, fine.ErrorNotExist
	default:
		return lh.inner.Readlink(ctx, h)
	}
}

func (lh *LazyHandler) Symlink(ctx context.Context, h *fine.RequestHeader, req *fine.SymlinkRequest) (*fine.EntryResponse, error) {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return nil, fine.ErrorIO
	case lh.inner == nil:
		return nil, fine.ErrorNotExist
	default:
		return lh.inner.Symlink(ctx, h, req)
	}
}

func (lh *LazyHandler) Mknod(ctx context.Context, h *fine.RequestHeader, req *fine.MknodRequest) (*fine.EntryResponse, error) {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return nil, fine.ErrorIO
	case lh.inner == nil:
		return nil, fine.ErrorNotExist
	default:
		return lh.inner.Mknod(ctx, h, req)
	}
}

func (lh *LazyHandler) Mkdir(ctx context.Context, h *fine.RequestHeader, req *fine.MkdirRequest) (*fine.EntryResponse, error) {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return nil, fine.ErrorIO
	case lh.inner == nil:
		return nil, fine.ErrorNotExist
	default:
		return lh.inner.Mkdir(ctx, h, req)
	}
}

func (lh *LazyHandler) Unlink(ctx context.Context, h *fine.RequestHeader, req *fine.UnlinkRequest) error {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return fine.ErrorIO
	case lh.inner == nil:
		return fine.ErrorNotExist
	default:
		return lh.inner.Unlink(ctx, h, req)
	}
}

func (lh *LazyHandler) Rmdir(ctx context.Context, h *fine.RequestHeader, req *fine.RmdirRequest) error {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return fine.ErrorIO
	case lh.inner == nil:
		return fine.ErrorNotExist
	default:
		return lh.inner.Rmdir(ctx, h, req)
	}
}

func (lh *LazyHandler) Rename(ctx context.Context, h *fine.RequestHeader, req *fine.RenameRequest) error {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return fine.ErrorIO
	case lh.inner == nil:
		return fine.ErrorNotExist
	default:
		return lh.inner.Rename(ctx, h, req)
	}
}

func (lh *LazyHandler) Link(ctx context.Context, h *fine.RequestHeader, req *fine.LinkRequest) (*fine.EntryResponse, error) {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return nil, fine.ErrorIO
	case lh.inner == nil:
		return nil, fine.ErrorNotExist
	default:
		return lh.inner.Link(ctx, h, req)
	}
}

func (lh *LazyHandler) Open(ctx context.Context, h *fine.RequestHeader, req *fine.OpenRequest) (*fine.OpenedResponse, error) {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return nil, fine.ErrorIO
	case lh.inner == nil:
		return nil, fine.ErrorNotExist
	default:
		return lh.inner.Open(ctx, h, req)
	}
}

func (lh *LazyHandler) Read(ctx context.Context, h *fine.RequestHeader, req *fine.ReadRequest) (*fine.ReadResponse, error) {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return nil, fine.ErrorIO
	case lh.inner == nil:
		return nil, fine.ErrorNotExist
	default:
		return lh.inner.Read(ctx, h, req)
	}
}

func (lh *LazyHandler) Write(ctx context.Context, h *fine.RequestHeader, req *fine.WriteRequest) (*fine.WriteResponse, error) {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return nil, fine.ErrorIO
	case lh.inner == nil:
		return nil, fine.ErrorNotExist
	default:
		return lh.inner.Write(ctx, h, req)
	}
}

func (lh *LazyHandler) Release(ctx context.Context, h *fine.RequestHeader, req *fine.ReleaseRequest) error {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return fine.ErrorIO
	case lh.inner == nil:
		return fine.ErrorNotExist
	default:
		return lh.inner.Release(ctx, h, req)
	}
}

func (lh *LazyHandler) Fsync(ctx context.Context, h *fine.RequestHeader, req *fine.FsyncRequest) error {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return fine.ErrorIO
	case lh.inner == nil:
		return fine.ErrorNotExist
	default:
		return lh.inner.Fsync(ctx, h, req)
	}
}

func (lh *LazyHandler) Flush(ctx context.Context, h *fine.RequestHeader, req *fine.FlushRequest) error {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return fine.ErrorIO
	case lh.inner == nil:
		return fine.ErrorNotExist
	default:
		return lh.inner.Flush(ctx, h, req)
	}
}

func (lh *LazyHandler) Opendir(ctx context.Context, h *fine.RequestHeader, req *fine.OpenRequest) (*fine.OpenedResponse, error) {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return nil, fine.ErrorIO
	case lh.inner == nil:
		return nil, fine.ErrorNotExist
	default:
		return lh.inner.Opendir(ctx, h, req)
	}
}

func (lh *LazyHandler) Readdir(ctx context.Context, h *fine.RequestHeader, req *fine.ReadRequest) (*fine.ReaddirResponse, error) {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return nil, fine.ErrorIO
	case lh.inner == nil:
		return nil, fine.ErrorNotExist
	default:
		return lh.inner.Readdir(ctx, h, req)
	}
}

func (lh *LazyHandler) Releasedir(ctx context.Context, h *fine.RequestHeader, req *fine.ReleaseRequest) error {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return fine.ErrorIO
	case lh.inner == nil:
		return fine.ErrorNotExist
	default:
		return lh.inner.Releasedir(ctx, h, req)
	}
}

func (lh *LazyHandler) Fsyncdir(ctx context.Context, h *fine.RequestHeader, req *fine.FsyncRequest) error {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return fine.ErrorIO
	case lh.inner == nil:
		return fine.ErrorNotExist
	default:
		return lh.inner.Fsyncdir(ctx, h, req)
	}
}

func (lh *LazyHandler) Access(ctx context.Context, h *fine.RequestHeader, req *fine.AccessRequest) error {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return fine.ErrorIO
	case lh.inner == nil:
		return fine.ErrorNotExist
	default:
		return lh.inner.Access(ctx, h, req)
	}
}

func (lh *LazyHandler) Create(ctx context.Context, h *fine.RequestHeader, req *fine.CreateRequest) (*fine.CreateResponse, error) {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return nil, fine.ErrorIO
	case lh.inner == nil:
		return nil, fine.ErrorNotExist
	default:
		return lh.inner.Create(ctx, h, req)
	}
}

func (lh *LazyHandler) BatchForget(ctx context.Context, h *fine.RequestHeader, req *fine.BatchForgetRequest) error {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return fine.ErrorIO
	case lh.inner == nil:
		return fine.ErrorNotExist
	default:
		return lh.inner.BatchForget(ctx, h, req)
	}
}

func (lh *LazyHandler) Lseek(ctx context.Context, h *fine.RequestHeader, req *fine.LseekRequest) (*fine.LseekResponse, error) {
	lh.mut.RLock()
	defer lh.mut.RUnlock()

	switch {
	case lh.closed:
		return nil, fine.ErrorIO
	case lh.inner == nil:
		return nil, fine.ErrorNotExist
	default:
		return lh.inner.Lseek(ctx, h, req)
	}
}
