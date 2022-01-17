package grpcfine

import (
	"context"
	"errors"
	"fmt"
	"io"
	sync "sync"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/rfratto/viceroy/internal/fine"
	"github.com/rfratto/viceroy/internal/fine/server"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

//go:generate protoc --go_out=. --go_opt=module=github.com/rfratto/viceroy/internal/fine/grpcfine --go-grpc_out=. --go-grpc_opt=module=github.com/rfratto/viceroy/internal/fine/grpcfine ./grpcfine.proto

// NewClientTransport returns a new fine.Transport from a gRPC Transport stream.
func NewClientTransport(stream Transport_StreamClient, codec Codec) fine.Transport {
	return &clientTransport{stream: stream, codec: codec}
}

type clientTransport struct {
	stream Transport_StreamClient
	codec  Codec

	rmut sync.Mutex
	wmut sync.Mutex
}

func (ct *clientTransport) RecvRequest() (fine.RequestHeader, fine.Request, error) {
	ct.rmut.Lock()
	defer ct.rmut.Unlock()

	rawReq, err := ct.stream.Recv()
	if s, ok := status.FromError(err); ok && s.Code() == codes.Canceled {
		return fine.RequestHeader{}, nil, io.EOF
	} else if err != nil {
		return fine.RequestHeader{}, nil, err
	}
	return ct.codec.DecodeRequest(rawReq)
}

func (ct *clientTransport) SendResponse(h fine.ResponseHeader, r fine.Response) (err error) {
	ct.wmut.Lock()
	defer ct.wmut.Unlock()
	rawResp, err := ct.codec.EncodeResponse(&h, r)
	if err != nil {
		return err
	}
	return ct.stream.Send(rawResp)
}

func (ct *clientTransport) Close() error {
	ct.wmut.Lock()
	defer ct.wmut.Unlock()
	return ct.stream.CloseSend()
}

// NewServerStreamHandler converts a Transport server stream into an
// server.Handler that can be used invoke commands. Call Wait to wait for the
// handler to close.
func NewServerStreamHandler(l log.Logger, stream Transport_StreamServer, codec Codec) (server.Handler, func(), error) {
	sh := &serverStreamHandler{
		log:    l,
		stream: stream,
		codec:  codec,
		closed: make(chan struct{}),
	}
	go sh.run()
	return sh, sh.wait, nil
}

type serverStreamHandler struct {
	log     log.Logger
	sendMut sync.Mutex
	stream  Transport_StreamServer
	codec   Codec
	closed  chan struct{}

	inflight sync.Map
}

var _ server.Handler = (*serverStreamHandler)(nil)

func (sh *serverStreamHandler) Init(ctx context.Context) error {
	resp, err := sh.handleRequest(ctx, &fine.RequestHeader{
		Op:        fine.OpInit,
		RequestID: 1,
		Node:      fine.RootNode,
	}, &fine.InitRequest{
		LatestVersion: fine.MinVersion,
		MaxReadahead:  1024,
		Flags:         fine.InitBigWrites,
	})
	if err != nil {
		return err
	}
	iresp, ok := resp.(*fine.InitResponse)
	if !ok {
		return fine.ErrorIO
	}
	if iresp.EarliestVersion != fine.MinVersion {
		return fmt.Errorf("grpc peer and local version do not match")
	}
	return nil
}

func (sh *serverStreamHandler) Close() error {
	var (
		h = fine.RequestHeader{Op: fine.OpDestroy, Node: fine.RootNode}
	)
	rawRequest, err := sh.codec.EncodeRequest(&h, nil)
	if err != nil {
		return fmt.Errorf("failed to encode close request: %w", err)
	}
	// Best-effort send the shutdown
	_ = sh.send(rawRequest)
	return nil
}

// wait blocks until the handler exits. This may block until the client closes
// their side of the connection..
func (sh *serverStreamHandler) wait() { <-sh.closed }

// run reads responses from the underlying stream and forwards them to blocked
// requests.
func (sh *serverStreamHandler) run() {
	defer close(sh.closed)
	defer level.Debug(sh.log).Log("msg", "RawHandlerClient exiting")

	for {
		rawResp, err := sh.stream.Recv()
		if s, ok := status.FromError(err); ok && s.Code() == codes.Canceled {
			return
		} else if errors.Is(err, io.EOF) {
			return
		} else if err != nil {
			level.Error(sh.log).Log("msg", "RawHandlerClient got read error from gRPC stream", "err", err)
			return
		}

		h, resp, err := sh.codec.DecodeResponse(rawResp)
		if err != nil {
			level.Error(sh.log).Log("msg", "failed to decode response from gRPC stream", "err", err)
			continue
		}

		val, found := sh.inflight.LoadAndDelete(h.RequestID)
		if found {
			val.(chan<- wireResponse) <- wireResponse{Header: h, Response: resp}
		} else if resp != nil && h.Op != fine.OpInterrupt {
			// The server will reply with interrupt notifications, but we don't wait
			// for responses on those. Log a warning for everything else.
			level.Warn(sh.log).Log("msg", "got response that doesn't match up to a request", "err", err)
			continue
		}
	}
}

type wireResponse struct {
	Header   fine.ResponseHeader
	Response fine.Response
}

func (sh *serverStreamHandler) handleRequest(ctx context.Context, h *fine.RequestHeader, req fine.Request) (resp fine.Response, err error) {
	rawRequest, err := sh.codec.EncodeRequest(h, req)
	if err != nil {
		return nil, fmt.Errorf("failed to encode request: %w", err)
	}

	// Register our channel to receive a response. Buffer it so the reading
	// goroutine can write even after we exit.
	respCh := make(chan wireResponse, 1)
	if _, loaded := sh.inflight.LoadOrStore(h.RequestID, (chan<- wireResponse)(respCh)); loaded {
		level.Warn(sh.log).Log("msg", "overwrote existing request handler; a non-unique ID was assigned to a request")
		sh.inflight.Store(h.RequestID, (chan<- wireResponse)(respCh))
	}
	defer sh.inflight.Delete(h.RequestID)

	if err := sh.send(rawRequest); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	// Forget requests don't have responses, so we can quit immediately.
	if h.Op == fine.OpForget {
		return nil, nil
	}

	select {
	case wr := <-respCh:
		var err error
		if wr.Header.Error != 0 {
			err = wr.Header.Error
		}
		return wr.Response, err

	case <-sh.closed:
		return nil, fine.ErrorAborted

	case <-ctx.Done():
		// We've been interrupted. Let's inform the gRPC server so it can cancel
		// jobs before we exited.
		sh.sendInterrupt(h.RequestID)
		return nil, fine.ErrorInterrupted
	}
}

func (sh *serverStreamHandler) sendInterrupt(id uint64) {
	// Form our interrupt request. We're not waiting for a response, so we don't
	// need to give a meangingful ID here.
	var (
		h = fine.RequestHeader{Op: fine.OpInterrupt, RequestID: 0}
		r = &fine.InterruptRequest{RequestID: id}
	)

	rawRequest, err := sh.codec.EncodeRequest(&h, r)
	if err != nil {
		level.Error(sh.log).Log("msg", "failed to generate interrupt request", "err", err)
		return
	}
	if err := sh.send(rawRequest); err != nil {
		level.Error(sh.log).Log("msg", "failed to send interrupt request", "err", err)
		return
	}
}

func (sh *serverStreamHandler) Lookup(ctx context.Context, hdr *fine.RequestHeader, req *fine.LookupRequest) (*fine.EntryResponse, error) {
	resp, err := sh.handleRequest(ctx, hdr, req)
	if err != nil {
		return nil, err
	}
	iresp, ok := resp.(*fine.EntryResponse)
	if !ok {
		return nil, fine.ErrorIO
	}
	return iresp, nil
}

func (sh *serverStreamHandler) Forget(ctx context.Context, hdr *fine.RequestHeader, req *fine.ForgetRequest) {
	_, _ = sh.handleRequest(ctx, hdr, req)
}

func (sh *serverStreamHandler) Getattr(ctx context.Context, hdr *fine.RequestHeader, req *fine.GetattrRequest) (*fine.AttrResponse, error) {
	resp, err := sh.handleRequest(ctx, hdr, req)
	if err != nil {
		return nil, err
	}
	iresp, ok := resp.(*fine.AttrResponse)
	if !ok {
		return nil, fine.ErrorIO
	}
	return iresp, nil
}

func (sh *serverStreamHandler) Setattr(ctx context.Context, hdr *fine.RequestHeader, req *fine.SetattrRequest) (*fine.AttrResponse, error) {
	resp, err := sh.handleRequest(ctx, hdr, req)
	if err != nil {
		return nil, err
	}
	iresp, ok := resp.(*fine.AttrResponse)
	if !ok {
		return nil, fine.ErrorIO
	}
	return iresp, nil
}

func (sh *serverStreamHandler) Readlink(ctx context.Context, hdr *fine.RequestHeader) (*fine.ReadlinkResponse, error) {
	resp, err := sh.handleRequest(ctx, hdr, nil)
	if err != nil {
		return nil, err
	}
	iresp, ok := resp.(*fine.ReadlinkResponse)
	if !ok {
		return nil, fine.ErrorIO
	}
	return iresp, nil
}

func (sh *serverStreamHandler) Symlink(ctx context.Context, hdr *fine.RequestHeader, req *fine.SymlinkRequest) (*fine.EntryResponse, error) {
	resp, err := sh.handleRequest(ctx, hdr, req)
	if err != nil {
		return nil, err
	}
	iresp, ok := resp.(*fine.EntryResponse)
	if !ok {
		return nil, fine.ErrorIO
	}
	return iresp, nil
}

func (sh *serverStreamHandler) Mknod(ctx context.Context, hdr *fine.RequestHeader, req *fine.MknodRequest) (*fine.EntryResponse, error) {
	resp, err := sh.handleRequest(ctx, hdr, req)
	if err != nil {
		return nil, err
	}
	iresp, ok := resp.(*fine.EntryResponse)
	if !ok {
		return nil, fine.ErrorIO
	}
	return iresp, nil
}

func (sh *serverStreamHandler) Mkdir(ctx context.Context, hdr *fine.RequestHeader, req *fine.MkdirRequest) (*fine.EntryResponse, error) {
	resp, err := sh.handleRequest(ctx, hdr, req)
	if err != nil {
		return nil, err
	}
	iresp, ok := resp.(*fine.EntryResponse)
	if !ok {
		return nil, fine.ErrorIO
	}
	return iresp, nil
}

func (sh *serverStreamHandler) Unlink(ctx context.Context, hdr *fine.RequestHeader, req *fine.UnlinkRequest) error {
	_, err := sh.handleRequest(ctx, hdr, req)
	return err
}

func (sh *serverStreamHandler) Rmdir(ctx context.Context, hdr *fine.RequestHeader, req *fine.RmdirRequest) error {
	_, err := sh.handleRequest(ctx, hdr, req)
	return err
}

func (sh *serverStreamHandler) Rename(ctx context.Context, hdr *fine.RequestHeader, req *fine.RenameRequest) error {
	_, err := sh.handleRequest(ctx, hdr, req)
	return err
}

func (sh *serverStreamHandler) Link(ctx context.Context, hdr *fine.RequestHeader, req *fine.LinkRequest) (*fine.EntryResponse, error) {
	resp, err := sh.handleRequest(ctx, hdr, req)
	if err != nil {
		return nil, err
	}
	iresp, ok := resp.(*fine.EntryResponse)
	if !ok {
		return nil, fine.ErrorIO
	}
	return iresp, nil
}

func (sh *serverStreamHandler) Open(ctx context.Context, hdr *fine.RequestHeader, req *fine.OpenRequest) (*fine.OpenedResponse, error) {
	resp, err := sh.handleRequest(ctx, hdr, req)
	if err != nil {
		return nil, err
	}
	iresp, ok := resp.(*fine.OpenedResponse)
	if !ok {
		return nil, fine.ErrorIO
	}
	return iresp, nil
}

func (sh *serverStreamHandler) Read(ctx context.Context, hdr *fine.RequestHeader, req *fine.ReadRequest) (*fine.ReadResponse, error) {
	resp, err := sh.handleRequest(ctx, hdr, req)
	if err != nil {
		return nil, err
	}
	iresp, ok := resp.(*fine.ReadResponse)
	if !ok {
		return nil, fine.ErrorIO
	}
	return iresp, nil
}

func (sh *serverStreamHandler) Write(ctx context.Context, hdr *fine.RequestHeader, req *fine.WriteRequest) (*fine.WriteResponse, error) {
	resp, err := sh.handleRequest(ctx, hdr, req)
	if err != nil {
		return nil, err
	}
	iresp, ok := resp.(*fine.WriteResponse)
	if !ok {
		return nil, fine.ErrorIO
	}
	return iresp, nil
}

func (sh *serverStreamHandler) Release(ctx context.Context, hdr *fine.RequestHeader, req *fine.ReleaseRequest) error {
	_, err := sh.handleRequest(ctx, hdr, req)
	return err
}

func (sh *serverStreamHandler) Fsync(ctx context.Context, hdr *fine.RequestHeader, req *fine.FsyncRequest) error {
	_, err := sh.handleRequest(ctx, hdr, req)
	return err
}

func (sh *serverStreamHandler) Flush(ctx context.Context, hdr *fine.RequestHeader, req *fine.FlushRequest) error {
	_, err := sh.handleRequest(ctx, hdr, req)
	return err
}

func (sh *serverStreamHandler) Opendir(ctx context.Context, hdr *fine.RequestHeader, req *fine.OpenRequest) (*fine.OpenedResponse, error) {
	resp, err := sh.handleRequest(ctx, hdr, req)
	if err != nil {
		return nil, err
	}
	iresp, ok := resp.(*fine.OpenedResponse)
	if !ok {
		return nil, fine.ErrorIO
	}
	return iresp, nil
}

func (sh *serverStreamHandler) Readdir(ctx context.Context, hdr *fine.RequestHeader, req *fine.ReadRequest) (*fine.ReaddirResponse, error) {
	resp, err := sh.handleRequest(ctx, hdr, req)
	if err != nil {
		return nil, err
	}
	iresp, ok := resp.(*fine.ReaddirResponse)
	if !ok {
		return nil, fine.ErrorIO
	}
	return iresp, nil
}

func (sh *serverStreamHandler) Releasedir(ctx context.Context, hdr *fine.RequestHeader, req *fine.ReleaseRequest) error {
	_, err := sh.handleRequest(ctx, hdr, req)
	return err
}

func (sh *serverStreamHandler) Fsyncdir(ctx context.Context, hdr *fine.RequestHeader, req *fine.FsyncRequest) error {
	_, err := sh.handleRequest(ctx, hdr, req)
	return err
}

func (sh *serverStreamHandler) Access(ctx context.Context, hdr *fine.RequestHeader, req *fine.AccessRequest) error {
	_, err := sh.handleRequest(ctx, hdr, req)
	return err
}

func (sh *serverStreamHandler) Create(ctx context.Context, hdr *fine.RequestHeader, req *fine.CreateRequest) (*fine.CreateResponse, error) {
	resp, err := sh.handleRequest(ctx, hdr, req)
	if err != nil {
		return nil, err
	}
	iresp, ok := resp.(*fine.CreateResponse)
	if !ok {
		return nil, fine.ErrorIO
	}
	return iresp, nil
}

func (sh *serverStreamHandler) BatchForget(ctx context.Context, hdr *fine.RequestHeader, req *fine.BatchForgetRequest) error {
	_, err := sh.handleRequest(ctx, hdr, req)
	return err
}

func (sh *serverStreamHandler) Lseek(ctx context.Context, hdr *fine.RequestHeader, req *fine.LseekRequest) (*fine.LseekResponse, error) {
	resp, err := sh.handleRequest(ctx, hdr, req)
	if err != nil {
		return nil, err
	}
	iresp, ok := resp.(*fine.LseekResponse)
	if !ok {
		return nil, fine.ErrorIO
	}
	return iresp, nil
}

func (sh *serverStreamHandler) send(req *Request) error {
	sh.sendMut.Lock()
	defer sh.sendMut.Unlock()
	return sh.stream.Send(req)
}
