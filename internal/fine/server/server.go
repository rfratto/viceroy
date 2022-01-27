package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/rfratto/viceroy/internal/fine"
	"github.com/rfratto/viceroy/internal/fine/fuse"
	"golang.org/x/sync/semaphore"
)

// Handler processes messages from a transport. Handler is passed to Serve,
// which will invoke methods as requests come in.
type Handler interface {
	// Init is called at the start of serving a handler.
	Init(context.Context) error

	// Close is called when closing a handler.
	Close() error

	Lookup(context.Context, *fine.RequestHeader, *fine.LookupRequest) (*fine.EntryResponse, error)
	Forget(context.Context, *fine.RequestHeader, *fine.ForgetRequest)
	Getattr(context.Context, *fine.RequestHeader, *fine.GetattrRequest) (*fine.AttrResponse, error)
	Setattr(context.Context, *fine.RequestHeader, *fine.SetattrRequest) (*fine.AttrResponse, error)
	Readlink(context.Context, *fine.RequestHeader) (*fine.ReadlinkResponse, error)
	Symlink(context.Context, *fine.RequestHeader, *fine.SymlinkRequest) (*fine.EntryResponse, error)
	Mknod(context.Context, *fine.RequestHeader, *fine.MknodRequest) (*fine.EntryResponse, error)
	Mkdir(context.Context, *fine.RequestHeader, *fine.MkdirRequest) (*fine.EntryResponse, error)
	Unlink(context.Context, *fine.RequestHeader, *fine.UnlinkRequest) error
	Rmdir(context.Context, *fine.RequestHeader, *fine.RmdirRequest) error
	Rename(context.Context, *fine.RequestHeader, *fine.RenameRequest) error
	Link(context.Context, *fine.RequestHeader, *fine.LinkRequest) (*fine.EntryResponse, error)
	Open(context.Context, *fine.RequestHeader, *fine.OpenRequest) (*fine.OpenedResponse, error)
	Read(context.Context, *fine.RequestHeader, *fine.ReadRequest) (*fine.ReadResponse, error)
	Write(context.Context, *fine.RequestHeader, *fine.WriteRequest) (*fine.WriteResponse, error)
	Release(context.Context, *fine.RequestHeader, *fine.ReleaseRequest) error
	Fsync(context.Context, *fine.RequestHeader, *fine.FsyncRequest) error
	Flush(context.Context, *fine.RequestHeader, *fine.FlushRequest) error
	Opendir(context.Context, *fine.RequestHeader, *fine.OpenRequest) (*fine.OpenedResponse, error)
	Readdir(context.Context, *fine.RequestHeader, *fine.ReadRequest) (*fine.ReaddirResponse, error)
	Releasedir(context.Context, *fine.RequestHeader, *fine.ReleaseRequest) error
	Fsyncdir(context.Context, *fine.RequestHeader, *fine.FsyncRequest) error
	Access(context.Context, *fine.RequestHeader, *fine.AccessRequest) error
	Create(context.Context, *fine.RequestHeader, *fine.CreateRequest) (*fine.CreateResponse, error)
	BatchForget(context.Context, *fine.RequestHeader, *fine.BatchForgetRequest) error
	Lseek(context.Context, *fine.RequestHeader, *fine.LseekRequest) (*fine.LseekResponse, error)
}

type Options struct {
	// ConcurrencyLimit is the maximum number of concurrent requests a Server can
	// run. If ConcurrencyLimit is 0, it will obtain its default from
	// DefaultOptions.
	//
	// If ConcurrencyLimit is less than 0, then it is treated as unlimited.
	ConcurrencyLimit int

	// RequestTimeout will force a request to abort after a given amount of time.
	// 0 means to never time out.
	RequestTimeout time.Duration

	// Transport is the transport used to read and write requests. Server takes
	// ownership of the Transport after passing to New; do not close directly.
	Transport fine.Transport

	// Handler is used for handling individual requests.
	Handler Handler

	// Optional middleware to preprocess requests with.
	Middleware []Middleware
}

// DefaultOptions provides defaults for Server.
var DefaultOptions = Options{
	ConcurrencyLimit: 64,
}

// Server is a FINE server, which asynchronously handles requests from a
// transport by passing them to a Handler.
type Server struct {
	log log.Logger
	o   Options

	// The middleware to execute before the handler
	mw      Middleware
	handler Invoker
}

// New creates a new Server. Read messages will be passed to Handler for
// handling.
//
// Call Serve to start the Server.
func New(l log.Logger, o Options) (*Server, error) {
	if o.Handler == nil {
		return nil, fmt.Errorf("Handler must be set")
	}
	if o.ConcurrencyLimit == 0 {
		o.ConcurrencyLimit = DefaultOptions.ConcurrencyLimit
	}

	// Build an optional chain of middleware to handle the request.
	chain := o.Middleware

	if l == nil {
		l = log.NewNopLogger()
	}
	return &Server{log: l, o: o, mw: chainMiddleware(chain), handler: handlerInvoker(o.Handler)}, nil
}

// Serve starts the server. Serve only returns if there was an error while
// serving or if ctx is canceled.
//
// Serve should not be called again after it has exited.
func (s *Server) Serve(ctx context.Context) error {
	// We want to close the transport and handler after we're done serving.
	// However, serving involves a non-cancelable call to our transport. We
	// launch a dedicated goroutine just for waiting for context to cancel,
	// and never return until it exits.
	exited := make(chan struct{})
	defer func() { <-exited }()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		defer close(exited)
		<-ctx.Done()

		level.Info(s.log).Log("msg", "fine server exiting")
		defer level.Debug(s.log).Log("msg", "fine server exited")

		if err := s.o.Transport.Close(); err != nil {
			level.Error(s.log).Log("msg", "error when closing transport", "err", err)
		}
		if err := s.o.Handler.Close(); err != nil {
			level.Error(s.log).Log("msg", "error when closing handler", "err", err)
		}
	}()

	// A new goroutine is spawned for each request we recieve from our transport.
	// A semaphore is used to restrict the total number of goroutines that may be
	// running concurrently up to the limit in the server's options.
	//
	// Running goroutines are tracked so they can be canceled. They will forcibly
	// be canceled on exit or whenever our Transport requests an interrupt for a
	// specific request.
	var (
		workerSema     *semaphore.Weighted
		runningWorkers sync.WaitGroup

		workerMut sync.Mutex
		workers   = make(map[uint64]context.CancelFunc)
	)
	if s.o.ConcurrencyLimit > 0 {
		// Only create a semaphore if we're configured to have a concurrency limit.
		// Otherwise it's free reign over the number of running goroutines.
		workerSema = semaphore.NewWeighted(int64(s.o.ConcurrencyLimit))
	}
	// scheduleWorker schedules a worker to handle the provided request. It
	// blocks until the worker is scheduled or if the context given to Serve was
	// canceled.
	scheduleWorker := func(header fine.RequestHeader, req fine.Request) {
		if workerSema != nil {
			err := workerSema.Acquire(ctx, 1)
			if err != nil {
				return
			}
		}

		workerMut.Lock()
		defer workerMut.Unlock()
		runningWorkers.Add(1)

		if cancel, exist := workers[header.RequestID]; exist && cancel != nil {
			level.Warn(s.log).Log("msg", "existing goroutine with same request ID exists; old worker will be canceled")
			cancel()
		}

		workerCtx, cancel := context.WithCancel(ctx)
		workers[header.RequestID] = cancel

		done := func() {
			// Immediately free up the sempahore and the count of running workers.
			// These should be done before trying to grab the mutex, otherwise our
			// defer below will deadlock.
			workerSema.Release(1)
			runningWorkers.Done()

			workerMut.Lock()
			defer workerMut.Unlock()
			delete(workers, header.RequestID)
		}
		// Run the worker.
		go handleRequest(workerCtx, s, header, req, done)
	}
	// stopWorker stops a running worker. stopped will be true when workerID was
	// found and it was stopped.
	stopWorker := func(workerID uint64) (stopped bool) {
		workerMut.Lock()
		defer workerMut.Unlock()

		cancel, ok := workers[workerID]
		if !ok {
			return false
		}
		cancel()
		delete(workers, workerID)
		return true
	}
	defer func() {
		// Stop all of our workers.
		workerMut.Lock()
		defer workerMut.Unlock()
		for key, cancel := range workers {
			cancel()
			delete(workers, key)
		}
		runningWorkers.Wait()
	}()

	// The first protocol message sohuld always be an Init. Inits may be sent
	// multiple times while the peers are agreeing on a protocol version to use.
	// Until the handshake completes, no other request will be processed.
	var didHandshake bool

	for {
		// Do an early return if our context has been canceled.
		if ctx.Err() != nil {
			level.Debug(s.log).Log("msg", "context canceled, breaking out of server read loop")
			return nil
		}

		header, req, err := s.o.Transport.RecvRequest()
		if errors.Is(err, io.EOF) {
			level.Debug(s.log).Log("msg", "got EOF from transport; exiting")
			return nil
		} else if err != nil {
			level.Error(s.log).Log("msg", "got error from transport; exiting", "err", err)
			return err
		}

		switch header.Op {
		default:
			if !didHandshake {
				level.Warn(s.log).Log("msg", "ignoring unexpected message sent before fine handshake completed", "op", header.Op, "op_val", int(header.Op))
				continue
			}
			scheduleWorker(header, req)

		case fine.OpInit:
			req, _ := req.(*fine.InitRequest)
			if req == nil {
				level.Error(s.log).Log("msg", "protocol error: got init request without request payload")
				return fmt.Errorf("missing init message payload from peer")
			}
			level.Debug(s.log).Log("msg", "got handshake request")

			if didHandshake {
				level.Warn(s.log).Log("msg", "ignoring unexpected post-handshake init message")
				continue
			}
			var err error
			didHandshake, err = s.processHandshake(header, req)
			if err == nil && didHandshake {
				err = s.o.Handler.Init(ctx)
			}
			if err != nil {
				return err
			}

		case fine.OpDestroy:
			level.Debug(s.log).Log("msg", "receieved shutdown request from peer")
			s.sendResponse(responseHeader(header, nil), nil)
			return nil

		case fine.OpInterrupt:
			req, _ := req.(*fine.InterruptRequest)
			if req == nil {
				level.Error(s.log).Log("msg", "protocol error: got interrupt request without request payload")
				return fmt.Errorf("missing interrupt message payload from peer")
			}
			level.Debug(s.log).Log("msg", "received interrupt request from peer", "id", req.RequestID)
			respHeader := responseHeader(header, nil)
			if !stopWorker(req.RequestID) {
				respHeader.Error = fine.ErrorInvalid
			}
			s.sendResponse(respHeader, nil)
		}
	}
}

func handleRequest(ctx context.Context, s *Server, header fine.RequestHeader, req fine.Request, done func()) {
	defer done()

	if s.o.RequestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.o.RequestTimeout)
		defer cancel()
	}

	resp, err := s.mw.HandleRequest(ctx, &header, req, s.handler)
	if header.Op == fine.OpForget {
		// Forgets don't generate responses.
		return
	}
	s.sendResponse(responseHeader(header, err), resp)
}

func (s *Server) sendResponse(h fine.ResponseHeader, resp fine.Response) {
	err := s.o.Transport.SendResponse(h, resp)
	if err != nil {
		level.Error(s.log).Log("msg", "failed to write response to transport", "err", err)
	}
}

func responseHeader(req fine.RequestHeader, err error) fine.ResponseHeader {
	return fine.ResponseHeader{
		Op:        req.Op,
		RequestID: req.RequestID,
		Error:     errorForResponse(err),
	}
}

func errorForResponse(err error) fine.Error {
	if err == nil {
		return 0
	}

	// Check for common system-level errors.
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fine.ErrorAborted
	case errors.Is(err, context.Canceled):
		return fine.ErrorInterrupted
	case os.IsNotExist(err):
		return fine.ErrorNotExist
	case os.IsPermission(err):
		return fine.ErrorNotPermitted
	case errors.Is(err, os.ErrNotExist):
		return fine.ErrorNotExist
	case errors.Is(err, io.EOF):
		return 0
	}

	var fe fine.Error
	if errors.As(err, &fe) {
		return fe
	}
	return fine.ErrorIO
}

// processHandshake processes the hanshake sent by the peer. If complete is
// false, the handshake is expected to be sent again.
func (s *Server) processHandshake(header fine.RequestHeader, init *fine.InitRequest) (complete bool, err error) {
	resp := &fine.InitResponse{
		EarliestVersion:     fine.MinVersion,
		MaxReadahead:        init.MaxReadahead,
		MaxWrite:            fuse.MaxWrite,
		MaxBackground:       1<<16 - 1,
		CongestionThreshold: (1<<16 - 1) * 3 / 4,
		MaxPages:            uint16(32*syscall.Getpagesize() + int(fuse.MaxWrite)),
		Flags:               init.Flags & fine.InitBigWrites & fine.InitAsyncRead & fine.InitAsyncDIO & fine.InitParallelDirOps & fine.InitMaxPages,
	}

	if init.LatestVersion.Major > fine.MinVersion.Major {
		// Kernel is too new. Let's tell it which version we support.
		s.sendResponse(responseHeader(header, nil), resp)
		return false, nil
	}
	if init.LatestVersion.Major < fine.MinVersion.Major {
		return false, fmt.Errorf("peer version %s too old for local version %s", init.LatestVersion, fine.MinVersion)
	}
	if init.LatestVersion.Minor < fine.MinVersion.Minor {
		level.Warn(s.log).Log(
			"msg", "peer version doesn't match local version. things may subtly break",
			"peer", init.LatestVersion, "local", fine.MinVersion,
		)
	}

	s.sendResponse(responseHeader(header, nil), resp)
	return true, nil
}
