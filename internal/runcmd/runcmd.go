// Package runcmd can run commands.
package runcmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/rfratto/viceroy/internal/viceroypb"
	"google.golang.org/grpc"
)

type Options struct {
	ServerAddr     string
	Request        *viceroypb.CommandRequest
	Stdout, Stderr io.Writer
}

func Run(o Options) (statusCode int, err error) {
	conn, err := grpc.Dial(o.ServerAddr, grpc.WithInsecure())
	if err != nil {
		return 1, fmt.Errorf("failed to connect to viceroyd: %w", err)
	}
	var runner = viceroypb.NewRunnerClient(conn)

	hdl, err := runner.CreateCommand(context.Background(), o.Request)
	if err != nil {
		return 1, fmt.Errorf("failed to run command: %w", err)
	}

	defer func() {
		_, _ = runner.DeleteCommand(context.Background(), hdl)
	}()

	var tailerWg sync.WaitGroup
	tailerWg.Add(1)
	defer tailerWg.Wait()

	go func() {
		defer tailerWg.Done()

		tailer, err := runner.TailCommand(context.Background(), hdl)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to tail clang output: %s", err)
			return
		}
		for {
			payload, err := tailer.Recv()
			if errors.Is(err, io.EOF) {
				return
			} else if err != nil {
				fmt.Fprintf(os.Stderr, "failed to tail clang output: %s", err)
				return
			}

			switch payload.Stream {
			default:
				_, _ = io.Copy(o.Stdout, bytes.NewReader(payload.Data))
			case viceroypb.Stream_STREAM_STDERR:
				_, _ = io.Copy(o.Stderr, bytes.NewReader(payload.Data))
			}
		}
	}()

	// Start compile and wait for it to end.
	status, err := runner.StartCommand(context.Background(), hdl)
	if err != nil {
		return 1, fmt.Errorf("failed to run clang: %w", err)
	}

	return int(status.ExitCode), nil
}
