package container

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	stdlog "log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/ory/dockertest/v3"
	dc "github.com/ory/dockertest/v3/docker"
	"github.com/rfratto/viceroy/internal/fine/grpcfine"
	"github.com/rfratto/viceroy/internal/fine/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

var dockerPool *dockertest.Pool

const containerName = "viceroyworker-container-test"

// TestMain builds the container before running any of the tests.
func TestMain(m *testing.M) {
	wd, err := os.Getwd()
	if err != nil {
		stdlog.Fatalf("failed to get wd: %s", err)
	}

	root, err := filepath.Abs(filepath.Join(wd, ".."))
	if err != nil {
		stdlog.Fatalf("failed to get root dir: %s", err)
	}

	// Make our container so everything can pass
	cmd := exec.Command("make", "container")
	cmd.Env = append(
		os.Environ(),
		"CONTAINER_NAME="+containerName,
		"DOCKER_BUILD_FLAGS=-q",
		"GOOS=linux",
		"GOARCH="+runtime.GOARCH,
	)
	cmd.Dir = root
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		stdlog.Fatalf("make failed: %s", err)
	}

	dockerPool, err = dockertest.NewPool("")
	if err != nil {
		stdlog.Fatalf("dockertest pool failed: %s", err)
	}

	os.Exit(m.Run())
}

// TestHostAccess ensures that the container is able to access host files.
func TestHostAccess(t *testing.T) {
	l := log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	l = log.With(l, "test", t.Name())

	tmpFile, _ := newTestFile(t)

	cc, exec := runContainer(t)
	newPassthroughServ(t, l, cc)

	execResult, err := exec("cat", filepath.Join("/mnt/voverlay", tmpFile))
	require.NoError(t, err)
	require.Equal(t, "", execResult.Stderr)
	require.Equal(t, "Hello, world!", execResult.Stdout)
}

func newPassthroughServ(t *testing.T, l log.Logger, cc *grpc.ClientConn) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	transportCli := grpcfine.NewTransportClient(cc)
	transportStream, err := transportCli.Stream(ctx)
	require.NoError(t, err)

	transport := grpcfine.NewClientTransport(transportStream, grpcfine.MsgpackCodec())
	fsys, err := server.New(l, server.Options{
		ConcurrencyLimit: server.DefaultOptions.ConcurrencyLimit,
		RequestTimeout:   15 * time.Second,
		Transport:        transport,
		Handler:          server.Passthrough(l, "/"),
		Middleware:       []server.Middleware{server.NewLoggingMiddleware(l)},
	})
	require.NoError(t, err)

	go func() {
		assert.NoError(t, fsys.Serve(ctx))
	}()
	time.Sleep(time.Second)
}

func newTestFile(t *testing.T) (fileName string, dirName string) {
	t.Helper()

	tmpFile, err := os.CreateTemp(t.TempDir(), "local-*")
	require.NoError(t, err)
	_, err = io.Copy(tmpFile, strings.NewReader("Hello, world!"))
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())

	stdlog.Println("Temporary file created at", tmpFile.Name())

	return tmpFile.Name(), filepath.Dir(tmpFile.Name())
}

// TestOverlay ensures that the container is able to overlay host files.
func TestOverlay(t *testing.T) {
	l := log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	l = log.With(l, "test", t.Name())

	tmpFile, tmpDir := newTestFile(t)

	cc, exec := runContainer(t)

	{
		// Create the overlayed file. We need to make the directory first before
		// we can write to the file.
		_, err := exec("mkdir", "-p", tmpDir)
		require.NoError(t, err)

		_, err = exec("sh", "-c", fmt.Sprintf(`echo -n "Overlayed" > %s`, tmpFile))
		require.NoError(t, err)

		execResult, err := exec("cat", tmpFile)
		require.NoError(t, err)
		require.Equal(t, "", execResult.Stderr)
		require.Equal(t, "Overlayed", execResult.Stdout)
	}

	newPassthroughServ(t, l, cc)

	execResult, err := exec("cat", filepath.Join("/mnt/voverlay", tmpFile))
	require.NoError(t, err)
	require.Equal(t, "", execResult.Stderr)
	require.Equal(t, "Overlayed", execResult.Stdout)
}

// TestCombineDirs ensures that ls will combine directories that exist on both machines.
func TestCombineDirs(t *testing.T) {
	l := log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	l = log.With(l, "test", t.Name())

	tmpFile, tmpDir := newTestFile(t)

	cc, exec := runContainer(t)

	{
		// Create a second file in our folder.
		_, err := exec("mkdir", "-p", tmpDir)
		require.NoError(t, err)

		containerFile := filepath.Join(tmpDir, "another")
		_, err = exec("sh", "-c", fmt.Sprintf(`echo -n "Another file" > %s`, containerFile))
		require.NoError(t, err)
	}

	newPassthroughServ(t, l, cc)

	execResult, err := exec("ls", filepath.Join("/mnt/voverlay", tmpDir))
	require.NoError(t, err)
	require.Equal(t, "", execResult.Stderr)
	require.Equal(t, fmt.Sprintf("another\n%s\n", filepath.Base(tmpFile)), execResult.Stdout)
}

// TestPreferHostCreate ensures that new files are created on the host machine.
func TestPreferHostCreate(t *testing.T) {
	l := log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	l = log.With(l, "test", t.Name())

	_, tmpDir := newTestFile(t)

	cc, exec := runContainer(t)

	{
		// Create a second file in our folder.
		_, err := exec("mkdir", "-p", tmpDir)
		require.NoError(t, err)
	}

	newPassthroughServ(t, l, cc)

	_, err := exec("sh", "-c", fmt.Sprintf(`echo -n "New file" > %s`, filepath.Join("/mnt/voverlay", tmpDir, "newfile")))
	require.NoError(t, err)

	bb, err := ioutil.ReadFile(filepath.Join(tmpDir, "newfile"))
	require.NoError(t, err)
	require.Equal(t, "New file", string(bb))
}

// TestRename ensures that file renames work.
func TestRename(t *testing.T) {
	l := log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	l = log.With(l, "test", t.Name())

	_, tmpDir := newTestFile(t)

	cc, exec := runContainer(t)
	newPassthroughServ(t, l, cc)

	require.NoError(t, os.MkdirAll(tmpDir, 0777))

	level.Debug(l).Log("msg", "executing test script")
	execResult, err := exec("bash", "-c", fmt.Sprintf(`
    set -euo pipefail
    touch %[1]s/testfile 
    touch %[1]s/testfile.tmp
    echo -n "testing" > %[1]s/testfile.tmp
    mv %[1]s/testfile.tmp %[1]s/testfile
    cat %[1]s/testfile
  `, filepath.Join("/mnt/voverlay", tmpDir)))

	level.Debug(l).Log("msg", "checking local file")
	_, staterr := os.Stat(filepath.Join(tmpDir, "testfile"))
	require.NoError(t, staterr, "testfile was not moved")

	require.NoError(t, err)
	require.Equal(t, "", execResult.Stderr)
	require.Equal(t, "testing", execResult.Stdout)
}

// runContainer runs the viceroyworkerd container and returns a client connection once
// it's up and running.
func runContainer(t *testing.T) (*grpc.ClientConn, execFunc) {
	t.Helper()

	res, err := dockerPool.RunWithOptions(
		&dockertest.RunOptions{
			Repository: containerName,
			Tag:        "latest",
			CapAdd:     []string{"SYS_ADMIN"},
			Env:        []string{"LOG_LEVEL=debug", "VICEROYFS_LOG_REQUESTS=1"},
		},
		func(hc *dc.HostConfig) {
			hc.Devices = append(hc.Devices, dc.Device{
				PathOnHost:        "/dev/fuse",
				PathInContainer:   "/dev/fuse",
				CgroupPermissions: "rwm",
			})
		},
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		err := dockerPool.Purge(res)
		require.NoError(t, err)
	})

	var cc *grpc.ClientConn
	err = dockerPool.Retry(func() error {
		addr := fmt.Sprintf("localhost:%s", res.GetPort("13613/tcp"))
		var err error
		cc, err = grpc.Dial(addr, grpc.WithBlock(), grpc.WithInsecure())
		return err
	})
	require.NoError(t, err)

	res.Exec(nil, dockertest.ExecOptions{})

	go func() {
		err := dockerPool.Client.Logs(dc.LogsOptions{
			Context:   context.Background(),
			Container: res.Container.ID,

			Stderr:            true,
			Stdout:            true,
			OutputStream:      os.Stdout,
			ErrorStream:       os.Stderr,
			Follow:            true,
			InactivityTimeout: time.Minute,
			RawTerminal:       false,
		})
		if err != nil {
			fmt.Println("Failed to get container logs", err)
		}
	}()

	return cc, makeExecFunc(res)
}

func makeExecFunc(res *dockertest.Resource) execFunc {
	return func(cmd ...string) (*execResult, error) {
		var stdout, stderr bytes.Buffer

		exitCode, err := res.Exec(cmd, dockertest.ExecOptions{
			StdOut: &stdout,
			StdErr: &stderr,
		})
		if err != nil {
			return nil, err
		} else if exitCode != 0 {
			return nil, fmt.Errorf("program exited with status %d: %s", exitCode, stderr.String())
		}

		return &execResult{
			Stdout: stdout.String(),
			Stderr: stderr.String(),
		}, nil
	}
}

type execFunc = func(cmd ...string) (*execResult, error)

type execResult struct {
	Stdout, Stderr string
}
