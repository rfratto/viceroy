// Package viceroyworkerd implements the viceroy worker daemon.
package viceroyworkerd

import (
	"context"
	"fmt"
	"net"
	"net/url"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/mitchellh/go-homedir"
	"github.com/rfratto/viceroy/internal/viceroypb"
	"google.golang.org/grpc"
)

// DefaultOptions is the set of defaults for viceroyworkerd.
var DefaultOptions = Options{
	ListenAddr: "tcp://0.0.0.0:12194",
}

type Options struct {
	ListenAddr      string // Address to listen for client connections.
	LocalExecChroot string // Chroot for commands
}

// Daemon is the vicerwoy worker daemon. Daemon exposes a gRPC API.
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

	viceroypb.RegisterRunnerServer(srv, newLocalRunner(l, o))
	return d, nil
}

// Start starts d and doesn't return until it stops or there's an error.
func (d *Daemon) Start() error {
	level.Info(d.log).Log("msg", "starting viceroyworkerd", "listen_addr", d.lis.Addr().String())
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
