// Package viceroyd implements the viceroy daemon. viceroyd manages workers and
// proxies build commands to running workers.
package viceroyd

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/mitchellh/go-homedir"
	"github.com/rfratto/viceroy/internal/fine/grpcfine"
	"github.com/rfratto/viceroy/internal/fine/server"
	"github.com/rfratto/viceroy/internal/viceroypb"
	"google.golang.org/grpc"
)

// DefaultOptions is the set of defaults for viceroyd.
var DefaultOptions = Options{
	ListenAddr:               "tcp://0.0.0.0:12194",
	ProvisionerContainer:     "rfratto/viceroy:latest",
	ProvisionerContainerName: "viceroy-worker",
}

type Options struct {
	ListenAddr               string // Address to listen for client connections.
	ProvisionerContainer     string // Container to use for provisioning
	ProvisionerContainerName string // Container name to use for provisioning
}

// Daemon is the viceroy daemon. Daemon exposes a gRPC API.
type Daemon struct {
	log  log.Logger
	lis  net.Listener
	srv  *grpc.Server
	opts Options

	ctx    context.Context
	cancel context.CancelFunc
}

// New creates a new Daemon.
func New(l log.Logger, o Options) (d *Daemon, err error) {
	u, err := url.Parse(o.ListenAddr)
	if err != nil {
		return nil, fmt.Errorf("cannot parse listen addr %q as url: %w", o.ListenAddr, err)
	}

	address, err := homedir.Expand(u.Host + u.Path)
	if err != nil {
		return nil, fmt.Errorf("invalid listen addr: %w", err)
	}

	lis, err := net.Listen(u.Scheme, address)
	if err != nil {
		return nil, fmt.Errorf("cannot open %s listener %s: %w", u.Scheme, address, err)
	}

	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(loggingUnaryInterceptor(l)),
		grpc.ChainStreamInterceptor(loggingStreamingInterceptor(l)),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		if err != nil {
			cancel()
		}
	}()

	d = &Daemon{
		log:  l,
		lis:  lis,
		srv:  srv,
		opts: o,

		ctx:    ctx,
		cancel: cancel,
	}

	provisioner, err := newProvisioner(l, o, d.mountHost)
	if err != nil {
		return nil, fmt.Errorf("failed to create provisioner: %w", err)
	}

	commandRunner, err := newProvisionerRunner(l, provisioner)
	if err != nil {
		return nil, fmt.Errorf("failed to create compiler server: %w", err)
	}

	viceroypb.RegisterRunnerServer(srv, commandRunner)
	return d, nil
}

func (d *Daemon) mountHost(c *Container) (err error) {
	// TODO(rfratto): since we expect the container to have a grpcfine server, we
	// can move this to the provisioner instead.
	l := log.With(d.log, "container", c.ID)

	transportCli := grpcfine.NewTransportClient(c.FS)
	transportStream, err := transportCli.Stream(d.ctx)
	if err != nil {
		return fmt.Errorf("connecting filesystem: %w", err)
	}
	defer func() {
		// Close our transport if we're returning with an error.
		if err != nil {
			level.Debug(l).Log("msg", "closing transport on return")
			_ = transportStream.CloseSend()
		}
	}()

	transport := grpcfine.NewClientTransport(transportStream, grpcfine.MsgpackCodec())

	fsys, err := server.New(l, server.Options{
		ConcurrencyLimit: server.DefaultOptions.ConcurrencyLimit,
		RequestTimeout:   15 * time.Second,
		Transport:        transport,
		Handler:          server.Passthrough(nil, "/"),
	})
	if err != nil {
		return fmt.Errorf("creating remote filesystem: %w", err)
	}

	go func() {
		level.Debug(l).Log("msg", "serving remote filesystem")
		err := fsys.Serve(d.ctx)
		level.Info(l).Log("msg", "remote filesystem exited", "err", err)
		// TODO(rfratto): what's the right thing to do here? Shut down the
		// container? Re-establish the filesystem?
		//
		// Shutting down the container seems the easiest to recover from, while
		// re-establishing might fail for any number of reasons.
	}()

	// TODO(rfratto): this should use a backoff to check for a specific mounted file in the overlay
	time.Sleep(1500 * time.Millisecond)
	return nil
}

// Start starts d and doesn't return until it stops or there's an error.
func (d *Daemon) Start() error {
	level.Info(d.log).Log("msg", "starting viceroyd", "listen_addr", d.lis.Addr().String())
	return d.srv.Serve(d.lis)
}

func (d *Daemon) Stop() error {
	d.cancel()
	d.srv.GracefulStop()
	return nil
}

func loggingUnaryInterceptor(l log.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (resp interface{}, err error) {
		level.Debug(l).Log("msg", "receieved gRPC request", "method", info.FullMethod)
		return handler(ctx, req)
	}
}

func loggingStreamingInterceptor(l log.Logger) grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		level.Debug(l).Log("msg", "receieved gRPC request", "method", info.FullMethod)
		return handler(srv, ss)
	}
}
