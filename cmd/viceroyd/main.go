// Command viceroyd implements the viceroy daemon. viceroy manages workers and
// proxies build requests to running workers.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "net/http/pprof" // anonymous import to get the pprof handler registered

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/gorilla/mux"
	"github.com/oklog/run"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rfratto/viceroy/internal/cmdutil"
	"github.com/rfratto/viceroy/viceroyd"
)

func main() {
	var (
		o  = viceroyd.DefaultOptions
		ll cmdutil.LogLevel
	)

	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.Var(&ll, "log.level", "Level to display logs at")

	fs.StringVar(&o.ListenAddr, "listen-addr", o.ListenAddr, "listen address for the viceroyd gRPC server")
	fs.StringVar(&o.ProvisionerContainer, "provisioner.container", o.ProvisionerContainer, "Container to use for provisioning")
	fs.StringVar(&o.ProvisionerContainerName, "provisioner.container-name", o.ProvisionerContainerName, "Container name to create for provisioning")

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error parsing flags: %s", err.Error())
		os.Exit(1)
	}

	var (
		stdout = log.NewSyncWriter(os.Stdout)
	)

	l := log.NewLogfmtLogger(stdout)
	l = level.NewFilter(l, ll.FilterOption())
	l = log.With(l, "ts", log.DefaultTimestamp, "caller", log.DefaultCaller)

	var group run.Group

	// Information server worker
	{
		lis, err := net.Listen("tcp", "0.0.0.0:8080")
		if err != nil {
			level.Error(l).Log("msg", "failed to create listener for HTTP server", "err", err)
			os.Exit(1)
		}

		r := mux.NewRouter()
		r.Handle("/metrics", promhttp.Handler())
		r.PathPrefix("/debug/pprof").Handler(http.DefaultServeMux)
		srv := http.Server{Handler: r}

		group.Add(func() error {
			err := srv.Serve(lis)
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		}, func(_ error) {
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer shutdownCancel()
			if err := srv.Shutdown(shutdownCtx); err != nil {
				_ = srv.Close()
			}
		})
	}

	// viceroyd worker
	{
		d, err := viceroyd.New(l, o)
		if err != nil {
			level.Error(l).Log("msg", "failed to create viceroyd", "err", err)
			os.Exit(1)
		}

		group.Add(func() error {
			return d.Start()
		}, func(_ error) {
			_ = d.Stop()
		})
	}

	// signal worker
	{
		ctx, cancel := context.WithCancel(context.Background())

		group.Add(func() error {
			ch := make(chan os.Signal, 2)
			signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
			defer signal.Stop(ch)

			select {
			case <-ch:
				level.Info(l).Log("msg", "received shutdown signal")
			case <-ctx.Done():
			}
			return nil
		}, func(_ error) {
			cancel()
		})
	}

	if err := group.Run(); err != nil {
		level.Error(l).Log("msg", "error running viceroyd", "err", err)
		os.Exit(1)
	}
}
