// Command viceroyworkerd implements the viceroy worker daemon. viceroy workers
// run build commands on behalf of a user.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
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
	"github.com/rfratto/viceroy/viceroyworkerd"
)

func main() {
	var (
		o  = viceroyworkerd.DefaultOptions
		ll cmdutil.LogLevel

		fineListenAddr string
	)

	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.Var(&ll, "log.level", "Level to display logs at")

	fs.StringVar(&o.ListenAddr, "listen-addr", o.ListenAddr, "listen address for the worker gRPC server")
	fs.StringVar(&o.LocalExecChroot, "overlay-dir", o.LocalExecChroot, "Mounts the viceroy overlay to the specified directory.")
	fs.StringVar(&fineListenAddr, "overlay-listen-addr", "0.0.0.0:13613", "Address for gRPC FINE used to create the overlay")

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error parsing flags: %s", err.Error())
		os.Exit(1)
	}

	var (
		stdout = log.NewSyncWriter(os.Stdout)
		stderr = log.NewSyncWriter(os.Stderr)
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

	// viceroyworkerd worker
	{
		d, err := viceroyworkerd.New(l, o)
		if err != nil {
			level.Error(l).Log("msg", "failed to create viceroyworkerd", "err", err)
			os.Exit(1)
		}

		group.Add(func() error {
			return d.Start()
		}, func(_ error) {
			_ = d.Stop()
		})
	}

	if o.LocalExecChroot == "" {
		fmt.Fprintln(os.Stderr, "-overlay-dir must be set")
		os.Exit(1)
	} else if fineListenAddr == "" {
		fmt.Fprintln(os.Stderr, "-overlay-listen-addr must be set")
		os.Exit(1)
	}

	// viceroyfs worker
	{
		// NOTE(rfratto): SIGKILL is sent when ctx is canceled.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		cmd := exec.CommandContext(
			ctx,
			"/usr/bin/viceroyfs",
			fmt.Sprintf("-log.level=%s", ll.String()),
			fmt.Sprintf("-listen.addr=%s", fineListenAddr),
			o.LocalExecChroot,
		)
		cmd.Stdout = stdout
		cmd.Stderr = stderr

		group.Add(func() error {
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("viceroyfs: %w", err)
			}
			return nil
		}, func(_ error) {
			if cmd.Process == nil {
				return
			}
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				cancel()
			}
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
		level.Error(l).Log("msg", "error running viceroyworkerd", "err", err)
		os.Exit(1)
	}
}
