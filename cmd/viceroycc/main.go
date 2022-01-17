// Command viceroycc proxies clang.
package main

import (
	"fmt"
	"os"

	"github.com/rfratto/viceroy/internal/runcmd"
	"github.com/rfratto/viceroy/internal/viceroypb"
)

func main() {
	exit, err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	os.Exit(exit)
}

func run() (statusCode int, err error) {
	serverAddr := "127.0.0.1:12194"
	if envAddr := os.Getenv("VICEROYD_ADDR"); envAddr != "" {
		serverAddr = envAddr
	}

	return runcmd.Run(runcmd.Options{
		ServerAddr: serverAddr,
		Request: &viceroypb.CommandRequest{
			Command: "viceroycc",
			Args:    os.Args[1:],
			Environ: os.Environ(),
			Wd: func() string {
				wd, _ := os.Getwd()
				return wd
			}(),
		},
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
}
