package viceroyworkerd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	pb "github.com/rfratto/viceroy/internal/viceroypb"
	uuid "github.com/satori/go.uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// localRunner implements the Runner service by locally invoking commands.
type localRunner struct {
	pb.UnimplementedRunnerServer

	log        log.Logger
	opts       Options
	commandMut sync.RWMutex
	commands   map[string]*localProcess
}

func newLocalRunner(l log.Logger, o Options) *localRunner {
	return &localRunner{log: l, commands: make(map[string]*localProcess), opts: o}
}

func (l *localRunner) CreateCommand(_ context.Context, req *pb.CommandRequest) (h *pb.CommandHandle, err error) {
	clangPath, err := exec.LookPath(req.GetCommand())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "command %q not installed", req.GetCommand())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		if err != nil {
			cancel()
		}
	}()

	var (
		stdout = newMultiTailer()
		stderr = newMultiTailer()
	)

	go func() {
		<-ctx.Done()
		stdout.Close()
	}()
	go func() {
		<-ctx.Done()
		stderr.Close()
	}()

	cmd := exec.CommandContext(ctx, clangPath, req.GetArgs()...)
	cmd.Env = append(req.GetEnviron(), "CC=clang", "CXX=clang") // TODO(rfratto): this should be sent by the client or handled by a wrapper
	cmd.Env = append(cmd.Env, "PATH="+os.Getenv("PATH"))        // Don't let PATH override local environment
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Dir = req.GetWd()

	level.Debug(l.log).Log("msg", "running command", "chroot", l.opts.LocalExecChroot, "cmd", strings.Join(cmd.Args, " "))
	if newRoot := l.opts.LocalExecChroot; newRoot != "" {
		err := chroot(cmd, newRoot)
		if err != nil {
			level.Error(l.log).Log("msg", "failed to set up chroot", "chroot", newRoot, "err", err)
			return nil, status.Error(codes.Internal, "failed to construct remote command")
		}
	}

	l.commandMut.Lock()
	defer l.commandMut.Unlock()

	lp := &localProcess{
		id:  l.nextUniqueID(),
		req: req,

		cmd:    cmd,
		stdout: stdout,
		stderr: stderr,

		ctx:    ctx,
		cancel: cancel,
	}
	l.commands[lp.id] = lp
	return &pb.CommandHandle{Id: lp.id}, nil
}

// nextUniqueID returns an unused ID. Must be called with commandMut held.
func (l *localRunner) nextUniqueID() string {
	for {
		id := uuid.NewV4().String()
		if _, found := l.commands[id]; !found {
			return id
		}
	}
}

func (l *localRunner) TailCommand(handle *pb.CommandHandle, srv pb.Runner_TailCommandServer) error {
	l.commandMut.RLock()
	lp, ok := l.commands[handle.GetId()]
	l.commandMut.RUnlock()
	if !ok {
		return status.Errorf(codes.NotFound, "command handle %s not found", handle.GetId())
	}

	var (
		sendMut       sync.Mutex
		streamReaders sync.WaitGroup
	)

	streamReaders.Add(2)
	defer streamReaders.Wait()

	readStream := func(r io.Reader, stream pb.Stream) {
		defer streamReaders.Done()

		buf := make([]byte, 1024)
		for {
			n, err := r.Read(buf)

			// Handle the potential existing data before we handle the error.
			if n > 0 {
				sendMut.Lock()
				err := srv.Send(&pb.TailCommandData{Data: buf[:n], Stream: stream})
				sendMut.Unlock()
				if err != nil {
					// Write failed; give up.
					return
				}
			}

			if errors.Is(err, io.EOF) && n != 0 {
				// Keep reading until we get EOF with no data.
				continue
			} else if err != nil {
				return
			}
		}
	}

	go readStream(lp.stdout.Reader(), pb.Stream_STREAM_STDOUT)
	go readStream(lp.stderr.Reader(), pb.Stream_STREAM_STDERR)
	return nil
}

func (l *localRunner) StartCommand(ctx context.Context, h *pb.CommandHandle) (*pb.CommandStatus, error) {
	l.commandMut.RLock()
	lp, ok := l.commands[h.GetId()]
	l.commandMut.RUnlock()

	if !ok {
		return nil, status.Errorf(codes.NotFound, "command handle %s not found", h.GetId())
	}
	defer lp.cancel()

	lp.cmdMut.Lock()
	err := lp.cmd.Start()
	lp.execErr = err
	lp.cmdMut.Unlock()
	if err != nil {
		// The comand couldn't be started; write the error out.
		fmt.Fprintln(lp.stderr, err)
	} else if err == nil {
		// Wait for the command to finish before getting its final status.
		_ = lp.cmd.Wait()
	}
	return lp.Status(), nil
}

func (l *localRunner) DeleteCommand(ctx context.Context, h *pb.CommandHandle) (*pb.Empty, error) {
	l.commandMut.Lock()
	lp, ok := l.commands[h.GetId()]
	l.commandMut.Unlock()
	if !ok {
		return nil, status.Errorf(codes.NotFound, "command handle %s not found", h.GetId())
	}

	lp.cmdMut.Lock()
	lp.aborted = true
	lp.cancel()
	lp.cmdMut.Unlock()

	l.commandMut.Lock()
	delete(l.commands, h.GetId())
	l.commandMut.Unlock()
	return &pb.Empty{}, nil
}

type localProcess struct {
	id  string
	req *pb.CommandRequest

	cmdMut  sync.Mutex
	cmd     *exec.Cmd
	aborted bool
	execErr error

	ctx    context.Context
	cancel context.CancelFunc

	stdout, stderr *multiTailer
}

func (lp *localProcess) Status() *pb.CommandStatus {
	lp.cmdMut.Lock()
	defer lp.cmdMut.Unlock()

	status := &pb.CommandStatus{
		Request: lp.req,
		State:   pb.CommandState_COMMAND_STATE_CREATED,
	}
	if lp.cmd.ProcessState != nil {
		switch {
		case lp.cmd.ProcessState.Exited():
			status.State = pb.CommandState_COMMAND_STATE_EXITED
			status.ExitCode = int32(lp.cmd.ProcessState.ExitCode())
		default:
			status.State = pb.CommandState_COMMAND_STATE_RUNNING
		}
	}
	if lp.execErr != nil {
		status.State = pb.CommandState_COMMAND_STATE_FAILED
		status.ExitCode = 1
	}
	if lp.aborted {
		status.State = pb.CommandState_COMMAND_STATE_ABORTED
	}

	return status
}

type multiTailer struct {
	mut  *sync.RWMutex
	cond *sync.Cond
	buf  []byte
	done bool
}

func newMultiTailer() *multiTailer {
	mut := &sync.RWMutex{}
	cond := sync.NewCond(mut.RLocker())

	return &multiTailer{mut: mut, cond: cond}
}

// Write writes b into the buffer. Sleeping goroutines will be woken up.
func (mt *multiTailer) Write(b []byte) (n int, err error) {
	cp := make([]byte, len(b))
	copy(cp, b)

	mt.mut.Lock()
	defer mt.mut.Unlock()
	if mt.done {
		return 0, io.ErrClosedPipe
	}

	mt.buf = append(mt.buf, cp...)
	mt.cond.Broadcast()
	return len(b), nil
}

// Close closes mt.
func (mt *multiTailer) Close() error {
	mt.mut.Lock()
	defer mt.mut.Unlock()
	mt.done = true
	mt.cond.Broadcast()
	return nil
}

// Reader returns a new reader that will read the existing data of mt followed
// by tailing it.
func (mt *multiTailer) Reader() io.Reader {
	return &multiTailerReader{multiTailer: mt}
}

type multiTailerReader struct {
	*multiTailer
	off int
}

// Read will read bytes into b. If there are no bytes available for reading,
// Read will block until there is new data.
func (mtr *multiTailerReader) Read(b []byte) (n int, err error) {
	mtr.mut.RLock()
	for len(mtr.buf[mtr.off:]) == 0 && !mtr.done {
		mtr.cond.Wait()
	}

	source := mtr.buf[mtr.off:]
	if len(b) < len(source) {
		source = source[:len(b)]
	}
	copy(b, source)
	mtr.off += len(source)

	if mtr.done {
		err = io.EOF
	}
	mtr.mut.RUnlock()
	return len(source), err
}
