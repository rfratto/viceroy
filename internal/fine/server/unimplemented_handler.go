package server

import (
	"context"

	"github.com/rfratto/viceroy/internal/fine"
)

// UnimplementedHandler implements Handler and returns ErrorUnimplemented for all requests.
type UnimplementedHandler struct{}

// Static type check test
var _ Handler = UnimplementedHandler{}

func (UnimplementedHandler) Init(context.Context) error {
	return nil
}

func (UnimplementedHandler) Close() error {
	return nil
}

func (UnimplementedHandler) Lookup(context.Context, *fine.RequestHeader, *fine.LookupRequest) (*fine.EntryResponse, error) {
	return nil, fine.ErrorUnimplemented
}

func (UnimplementedHandler) Forget(context.Context, *fine.RequestHeader, *fine.ForgetRequest) {
	// no-op
}

func (UnimplementedHandler) Getattr(context.Context, *fine.RequestHeader, *fine.GetattrRequest) (*fine.AttrResponse, error) {
	return nil, fine.ErrorUnimplemented
}

func (UnimplementedHandler) Setattr(context.Context, *fine.RequestHeader, *fine.SetattrRequest) (*fine.AttrResponse, error) {
	return nil, fine.ErrorUnimplemented
}

func (UnimplementedHandler) Readlink(context.Context, *fine.RequestHeader) (*fine.ReadlinkResponse, error) {
	return nil, fine.ErrorUnimplemented
}

func (UnimplementedHandler) Symlink(context.Context, *fine.RequestHeader, *fine.SymlinkRequest) (*fine.EntryResponse, error) {
	return nil, fine.ErrorUnimplemented
}

func (UnimplementedHandler) Mknod(context.Context, *fine.RequestHeader, *fine.MknodRequest) (*fine.EntryResponse, error) {
	return nil, fine.ErrorUnimplemented
}

func (UnimplementedHandler) Mkdir(context.Context, *fine.RequestHeader, *fine.MkdirRequest) (*fine.EntryResponse, error) {
	return nil, fine.ErrorUnimplemented
}

func (UnimplementedHandler) Unlink(context.Context, *fine.RequestHeader, *fine.UnlinkRequest) error {
	return fine.ErrorUnimplemented
}

func (UnimplementedHandler) Rmdir(context.Context, *fine.RequestHeader, *fine.RmdirRequest) error {
	return fine.ErrorUnimplemented
}

func (UnimplementedHandler) Rename(context.Context, *fine.RequestHeader, *fine.RenameRequest) error {
	return fine.ErrorUnimplemented
}

func (UnimplementedHandler) Link(context.Context, *fine.RequestHeader, *fine.LinkRequest) (*fine.EntryResponse, error) {
	return nil, fine.ErrorUnimplemented
}

func (UnimplementedHandler) Open(context.Context, *fine.RequestHeader, *fine.OpenRequest) (*fine.OpenedResponse, error) {
	return nil, fine.ErrorUnimplemented
}

func (UnimplementedHandler) Read(context.Context, *fine.RequestHeader, *fine.ReadRequest) (*fine.ReadResponse, error) {
	return nil, fine.ErrorUnimplemented
}

func (UnimplementedHandler) Write(context.Context, *fine.RequestHeader, *fine.WriteRequest) (*fine.WriteResponse, error) {
	return nil, fine.ErrorUnimplemented
}

func (UnimplementedHandler) Release(context.Context, *fine.RequestHeader, *fine.ReleaseRequest) error {
	return fine.ErrorUnimplemented
}

func (UnimplementedHandler) Fsync(context.Context, *fine.RequestHeader, *fine.FsyncRequest) error {
	return fine.ErrorUnimplemented
}

func (UnimplementedHandler) Flush(context.Context, *fine.RequestHeader, *fine.FlushRequest) error {
	return fine.ErrorUnimplemented
}

func (UnimplementedHandler) Opendir(context.Context, *fine.RequestHeader, *fine.OpenRequest) (*fine.OpenedResponse, error) {
	return nil, fine.ErrorUnimplemented
}

func (UnimplementedHandler) Readdir(context.Context, *fine.RequestHeader, *fine.ReadRequest) (*fine.ReaddirResponse, error) {
	return nil, fine.ErrorUnimplemented
}

func (UnimplementedHandler) Releasedir(context.Context, *fine.RequestHeader, *fine.ReleaseRequest) error {
	return fine.ErrorUnimplemented
}

func (UnimplementedHandler) Fsyncdir(context.Context, *fine.RequestHeader, *fine.FsyncRequest) error {
	return fine.ErrorUnimplemented
}

func (UnimplementedHandler) Access(context.Context, *fine.RequestHeader, *fine.AccessRequest) error {
	return fine.ErrorUnimplemented
}

func (UnimplementedHandler) Create(context.Context, *fine.RequestHeader, *fine.CreateRequest) (*fine.CreateResponse, error) {
	return nil, fine.ErrorUnimplemented
}

func (UnimplementedHandler) BatchForget(context.Context, *fine.RequestHeader, *fine.BatchForgetRequest) error {
	return fine.ErrorUnimplemented
}

func (UnimplementedHandler) Lseek(context.Context, *fine.RequestHeader, *fine.LseekRequest) (*fine.LseekResponse, error) {
	return nil, fine.ErrorUnimplemented
}
