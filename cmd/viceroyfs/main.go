//go:build linux

// Command viceroyfs mounts a filesytem and exposes the driver over gRPC.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	_ "net/http/pprof" // anonymous import to get the pprof handler registered

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rfratto/viceroy/internal/cmdutil"
	"github.com/rfratto/viceroy/internal/fine/fuse"
	"github.com/rfratto/viceroy/internal/fine/grpcfine"
	"github.com/rfratto/viceroy/internal/fine/server"
	"github.com/rfratto/viceroy/internal/voverlay"
	"go.uber.org/atomic"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	rootMountPath = "/mnt/root"
)

func main() {
	var (
		ll         cmdutil.LogLevel
		listenAddr string
	)

	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.Var(&ll, "log.level", "Level to display logs at")
	fs.StringVar(&listenAddr, "listen.addr", "127.0.0.1:9095", "address to listen for gRPC traffic on")

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error parsing flags: %s\n", err.Error())
		os.Exit(1)
	}

	if len(fs.Args()) != 1 {
		fmt.Fprintf(os.Stderr, "usage: %s [mountpoint]\n", os.Args[0])
		os.Exit(1)
	}

	l := log.NewLogfmtLogger(log.NewSyncWriter(os.Stdout))
	l = level.NewFilter(l, ll.FilterOption())
	l = log.With(l, "ts", log.DefaultTimestamp, "caller", log.DefaultCaller, "program", "viceroyfs")

	if err := run(l, listenAddr, fs.Arg(0)); err != nil {
		level.Error(l).Log("msg", "error during run", "err", err)
		os.Exit(1)
	}
}

func run(l log.Logger, grpcAddr string, mountPath string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var workers sync.WaitGroup
	defer workers.Wait()

	var lazyHandler server.LazyHandler

	// Information server worker
	{
		lis, err := net.Listen("tcp", "0.0.0.0:8081")
		if err != nil {
			level.Error(l).Log("msg", "failed to create listener for HTTP server", "err", err)
			os.Exit(1)
		}

		r := mux.NewRouter()
		r.Handle("/metrics", promhttp.Handler())
		r.PathPrefix("/debug/pprof").Handler(http.DefaultServeMux)
		srv := http.Server{Handler: r}

		workers.Add(1)
		go func() {
			defer workers.Done()
			defer cancel()
			level.Debug(l).Log("msg", "listening for http traffic", "addr", lis.Addr())
			if err := srv.Serve(lis); err != nil {
				level.Error(l).Log("msg", "http server exited with error", "err", err)
			}
		}()
		defer srv.Close()
	}

	// Root mount
	{
		if err := os.MkdirAll(rootMountPath, 0770); err != nil {
			return fmt.Errorf("creating root mount")
		}

		bindMounts := []struct{ From, To string }{
			{"/", rootMountPath},
			{"/etc/resolv.conf", filepath.Join(rootMountPath, "/etc/resolv.conf")},
			{"/etc/hostname", filepath.Join(rootMountPath, "/etc/hostname")},
			{"/etc/hosts", filepath.Join(rootMountPath, "/etc/hosts")},
		}
		for _, bm := range bindMounts {
			err := syscall.Mount(bm.From, bm.To, "", syscall.MS_BIND, "")
			if err != nil {
				return fmt.Errorf("creating %q mount: %w", bm.To, err)
			}
		}
		defer func() {
			for i := len(bindMounts) - 1; i >= 0; i-- {
				bm := bindMounts[i]
				err := syscall.Unmount(bm.To, syscall.MNT_DETACH)
				if err != nil {
					level.Warn(l).Log("msg", "failed to unmount", "mount", bm.To, "err", err)
				}
			}
		}()
	}

	// gRPC worker
	{
		lis, err := net.Listen("tcp", grpcAddr)
		if err != nil {
			return fmt.Errorf("failed to create network listener")
		}
		srv := grpc.NewServer()
		grpcfine.RegisterTransportServer(srv, &grpcTransport{
			log:  l,
			lazy: &lazyHandler,
		})

		workers.Add(1)
		go func() {
			defer workers.Done()
			defer cancel()
			level.Debug(l).Log("msg", "listening for grpc traffic", "addr", lis.Addr())
			if err := srv.Serve(lis); err != nil {
				level.Error(l).Log("msg", "grpc server exited with error", "err", err)
			}
		}()
		defer srv.Stop()
	}

	// FUSE worker
	{
		if err := os.MkdirAll(mountPath, 0770); err != nil {
			return fmt.Errorf("creating mount path")
		}

		transport, err := fuse.Mount(l, mountPath, fuse.DefaultPermissions())
		if err != nil {
			return fmt.Errorf("failed to create mount: %w", err)
		}

		var middleware []server.Middleware
		if os.Getenv("VICEROYFS_LOG_REQUESTS") != "" {
			middleware = append(middleware, server.NewLoggingMiddleware(l))
		}
		srv, err := server.New(l, server.Options{
			ConcurrencyLimit: server.DefaultOptions.ConcurrencyLimit,
			RequestTimeout:   15 * time.Second,
			Transport:        transport,
			Handler:          &lazyHandler,
			Middleware:       middleware,
		})
		if err != nil {
			return fmt.Errorf("failed to create userspace driver: %w", err)
		}

		workers.Add(1)
		go func() {
			defer workers.Done()
			defer cancel()
			level.Debug(l).Log("msg", "serving FUSE traffic", "dir", mountPath)
			if err := srv.Serve(ctx); err != nil {
				level.Error(l).Log("msg", "fs exited with error", "err", err)
			}
		}()
	}

	level.Info(l).Log("msg", "viceroyfs running in foreground, waiting for interrupt or error")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)

	select {
	case <-sig:
		level.Info(l).Log("msg", "interrupt signal received, exiting")
		return nil
	case <-ctx.Done():
		return fmt.Errorf("worker exited unexpectedly")
	}
}

type grpcTransport struct {
	grpcfine.UnimplementedTransportServer
	log log.Logger

	lazy *server.LazyHandler
	set  atomic.Bool
}

func (t *grpcTransport) Stream(stream grpcfine.Transport_StreamServer) error {
	if !t.set.CAS(false, true) {
		return status.Errorf(codes.AlreadyExists, "fs handler already registered")
	}
	defer t.set.Store(false)
	defer level.Debug(t.log).Log("msg", "gRPC transport terminated")

	clientHandler, wait, err := grpcfine.NewServerStreamHandler(t.log, stream, grpcfine.MsgpackCodec())
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "failed to handshake: %s", err)
	}
	defer clientHandler.Close()

	upper := server.Passthrough(nil, rootMountPath)
	overlay := voverlay.New(t.log, upper, clientHandler)

	if err := t.lazy.SetHandler(stream.Context(), overlay); err != nil {
		return err
	}

	// Wait until the client closes.
	level.Debug(t.log).Log("msg", "serving gRPC transport")
	defer level.Debug(t.log).Log("msg", "terminating gRPC transport")
	wait()
	return nil
}
