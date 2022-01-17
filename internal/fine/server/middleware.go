package server

import (
	"context"
	"fmt"

	"github.com/rfratto/viceroy/internal/fine"
)

// Middleware hooks into requests.
type Middleware interface {
	// HandleRequest handles an individual request.
	HandleRequest(ctx context.Context, hdr *fine.RequestHeader, req fine.Request, invoker Invoker) (fine.Response, error)
}

// Invoker is called by Middleware to complete requests.
type Invoker func(ctx context.Context, hdr *fine.RequestHeader, req fine.Request) (fine.Response, error)

// FuncMiddleware is a function that implements Middleware.
type FuncMiddleware func(ctx context.Context, hdr *fine.RequestHeader, req fine.Request, i Invoker) (fine.Response, error)

func (f FuncMiddleware) HandleRequest(ctx context.Context, h *fine.RequestHeader, req fine.Request, i Invoker) (fine.Response, error) {
	return f(ctx, h, req, i)
}

// handlerInvoker converts h into an Invoker.
func handlerInvoker(h Handler) Invoker {
	return func(ctx context.Context, header *fine.RequestHeader, req fine.Request) (resp fine.Response, err error) {
		switch header.Op {
		case fine.OpLookup:
			req, _ := req.(*fine.LookupRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			resp, err = h.Lookup(ctx, header, req)

		case fine.OpForget:
			// Unlike other requests, Forget has no response so we return immediately
			// once it's done.
			req, _ := req.(*fine.ForgetRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			h.Forget(ctx, header, req)

		case fine.OpGetattr:
			req, _ := req.(*fine.GetattrRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			resp, err = h.Getattr(ctx, header, req)

		case fine.OpSetattr:
			req, _ := req.(*fine.SetattrRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			resp, err = h.Setattr(ctx, header, req)

		case fine.OpReadlink:
			// Readlink has no request
			resp, err = h.Readlink(ctx, header)

		case fine.OpSymlink:
			req, _ := req.(*fine.SymlinkRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			resp, err = h.Symlink(ctx, header, req)

		case fine.OpMknod:
			req, _ := req.(*fine.MknodRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			resp, err = h.Mknod(ctx, header, req)

		case fine.OpMkdir:
			req, _ := req.(*fine.MkdirRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			resp, err = h.Mkdir(ctx, header, req)

		case fine.OpUnlink:
			req, _ := req.(*fine.UnlinkRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			err = h.Unlink(ctx, header, req)

		case fine.OpRmdir:
			req, _ := req.(*fine.RmdirRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			err = h.Rmdir(ctx, header, req)

		case fine.OpRename:
			req, _ := req.(*fine.RenameRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			err = h.Rename(ctx, header, req)

		case fine.OpLink:
			req, _ := req.(*fine.LinkRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			resp, err = h.Link(ctx, header, req)

		case fine.OpOpen:
			req, _ := req.(*fine.OpenRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			resp, err = h.Open(ctx, header, req)

		case fine.OpRead:
			req, _ := req.(*fine.ReadRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			resp, err = h.Read(ctx, header, req)

		case fine.OpWrite:
			req, _ := req.(*fine.WriteRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			resp, err = h.Write(ctx, header, req)

		case fine.OpRelease:
			req, _ := req.(*fine.ReleaseRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			err = h.Release(ctx, header, req)

		case fine.OpFsync:
			req, _ := req.(*fine.FsyncRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			err = h.Fsync(ctx, header, req)

		case fine.OpFlush:
			req, _ := req.(*fine.FlushRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			err = h.Flush(ctx, header, req)

		case fine.OpOpendir:
			req, _ := req.(*fine.OpenRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			resp, err = h.Opendir(ctx, header, req)

		case fine.OpReaddir:
			req, _ := req.(*fine.ReadRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			resp, err = h.Readdir(ctx, header, req)

		case fine.OpReleasedir:
			req, _ := req.(*fine.ReleaseRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			err = h.Releasedir(ctx, header, req)

		case fine.OpFsyncDir:
			req, _ := req.(*fine.FsyncRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			err = h.Fsyncdir(ctx, header, req)

		case fine.OpAccess:
			req, _ := req.(*fine.AccessRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			err = h.Access(ctx, header, req)

		case fine.OpCreate:
			req, _ := req.(*fine.CreateRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			resp, err = h.Create(ctx, header, req)

		case fine.OpBatchForget:
			req, _ := req.(*fine.BatchForgetRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			err = h.BatchForget(ctx, header, req)

		case fine.OpLseek:
			req, _ := req.(*fine.LseekRequest)
			if req == nil {
				err = fmt.Errorf("missing request body for %s: %w", header.Op, fine.ErrorInvalid)
				break
			}
			resp, err = h.Lseek(ctx, header, req)

		default:
			err = fmt.Errorf("unexpected opcode %q: %w", header.Op, fine.ErrorUnimplemented)
		}

		return resp, err
	}
}

type chainMiddleware []Middleware

func (c chainMiddleware) HandleRequest(ctx context.Context, h *fine.RequestHeader, req fine.Request, invoker Invoker) (fine.Response, error) {
	if len(c) == 0 {
		return invoker(ctx, h, req)
	}

	var (
		index        int
		chainInvoker Invoker
	)

	chainInvoker = func(ctx context.Context, h *fine.RequestHeader, req fine.Request) (fine.Response, error) {
		mw := c[index]
		index++

		var next Invoker
		if index == len(c) {
			next = invoker
		} else {
			next = chainInvoker
		}

		return mw.HandleRequest(ctx, h, req, next)
	}
	return chainInvoker(ctx, h, req)
}
