package fuse

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/rfratto/viceroy/internal/fine"
)

// MountOption customizes the filesystem mount.
type MountOption func(*mountConfig)

type mountConfig struct{ options map[string]string }

// getOptions converts the mount options to be compatible with fusermount's
// `-o` flag.
func (m mountConfig) getOptions() string {
	opts := make([]string, 0, len(m.options))
	for k, v := range m.options {
		opt := k
		if v != "" {
			opt += "=" + v
		}
		// Sanitize opt to remove unsafe characters
		if strings.Contains(opt, `,`) {
			opt = strings.ReplaceAll(opt, `,`, `\,`)
		}
		if strings.Contains(opt, `\`) {
			opt = strings.ReplaceAll(opt, `\`, `\\`)
		}
		opts = append(opts, opt)
	}
	return strings.Join(opts, ",")
}

// FilesystemName sets the fsname that is visible in the list of mounted
// filesystems.
func FSName(name string) MountOption {
	return func(mc *mountConfig) { mc.options["fsname"] = name }
}

// Subtype sets the subtype of the mount. Setting a subtype will have the full
// type appear as `fine.<subtype>`. The main type cannot be changed.
func Subtype(subtype string) MountOption {
	return func(mc *mountConfig) { mc.options["subtype"] = subtype }
}

// AllowOther allows other users to access the filesystem.
func AllowOther() MountOption {
	return func(mc *mountConfig) { mc.options["allow_other"] = "" }
}

// AllowDev enables interpreting character or block special devices on the
// filesystem.
func AllowDev() MountOption {
	return func(mc *mountConfig) { mc.options["dev"] = "" }
}

// AllowSUID allows SUID and SGID bits to take effect.
func AllowSUID() MountOption {
	return func(mc *mountConfig) { mc.options["suid"] = "" }
}

// DefaultPermissions requests for the kernel to enforce access control based
// on the file mode on files. Without this option, the driver itself must
// implement permission checking.
func DefaultPermissions() MountOption {
	return func(mc *mountConfig) { mc.options["default_permissions"] = "" }
}

// ReadOnly marks the mount as read-only.
func ReadOnly() MountOption {
	return func(mc *mountConfig) { mc.options["ro"] = "" }
}

// AllowNonEmptyMount allows mounting on top of a non-empty directory.
func AllowNonEmptyMount() MountOption {
	return func(mc *mountConfig) { mc.options["nonempty"] = "" }
}

// Mount creates a mountpoint at dir and returns the Conn to handle FUSE
// requests from it.
//
// Not all FUSE commands are supported.
func Mount(l log.Logger, dir string, opts ...MountOption) (fine.Transport, error) {
	cfg := mountConfig{options: map[string]string{}}
	for _, opt := range opts {
		opt(&cfg)
	}

	f, err := mount(l, dir, &cfg)
	if err != nil {
		return nil, err
	}

	return &devTransport{log: l, f: f, onClose: func() {
		level.Debug(l).Log("msg", "unmounting volume", "err", err)
		err := Unmount(dir)
		if err != nil {
			level.Error(l).Log("msg", "failed to automatically unmount on close", "err", err)
		} else {
			level.Debug(l).Log("msg", "volume unmounted", "err", err)
		}
	}}, nil
}

func Unmount(dir string) error {
	cmd := exec.Command("fusermount", "-z", "-u", dir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		output = bytes.TrimRight(output, "\n")
		if len(output) > 0 {
			err = fmt.Errorf("%w: %s", err, string(output))
		}
	}
	return err
}

func mount(l log.Logger, dir string, conf *mountConfig) (fd *os.File, err error) {
	// Create a pair of sockets. These sockets are temporary and are only used for
	// setting up our FUSE connection. One socket is handed to fusermount, and the
	// other is used locally to read control messages.
	fds, err := syscall.Socketpair(syscall.AF_FILE, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("socketpair error: %v", err)
	}

	writeFile := os.NewFile(uintptr(fds[0]), "fusermount-child-writes")
	defer writeFile.Close()

	readFile := os.NewFile(uintptr(fds[1]), "fusermount-parent-reads")
	defer readFile.Close()

	cmd := exec.Command(
		"fusermount",
		"-o", conf.getOptions(),
		"--",
		dir,
	)
	cmd.ExtraFiles = []*os.File{writeFile}
	cmd.Env = append(os.Environ(), "_FUSE_COMMFD=3")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("fusermount stdout: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("fusermount stderr: %v", err)
	}

	// Start our command. Since we're using StdoutPipe and StderrPipe, we must
	// launch goroutines to fully read their contents before calling cmd.Wait().
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("fusermount: %v", err)
	}

	var pipeWait sync.WaitGroup
	pipeWait.Add(2)

	readLogs := func(r io.Reader, l log.Logger, defLevel level.Value) {
		defer pipeWait.Done()
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			leveled := log.WithPrefix(l, level.Key(), defLevel)
			leveled.Log("msg", scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			level.Error(l).Log("msg", "failed to read scanner", "err", err)
		}
	}
	go readLogs(stdout, l, level.DebugValue())
	go readLogs(stderr, l, level.WarnValue())

	pipeWait.Wait()
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("fusermount failed")
	}

	// The read half of our socket pair should now be usaable for getting control
	// messages.
	c, err := net.FileConn(readFile)
	if err != nil {
		return nil, fmt.Errorf("FileConn from fusermount socket: %v", err)
	}
	defer c.Close()
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return nil, fmt.Errorf("unexpected FileConn type; expected UnixConn, got %T", c)
	}

	// Finally, we need to read from the socket to finish setting up our
	// connection. We're looking for a control message which gives us a file
	// descriptor that represents our connection to /dev/fine.
	//
	// Note that we only care about reading the out-of-band data here, and
	// we oversize the buffer a little for compatibility; no more than 24
	// bytes should be actually read.
	var oob = make([]byte, 32)
	_, oobLen, _, _, _ := uc.ReadMsgUnix(make([]byte, 32), oob)

	controlMsgs, err := syscall.ParseSocketControlMessage(oob[:oobLen])
	if err != nil {
		return nil, fmt.Errorf("ParseSocketControlMessage: %v", err)
	}
	if len(controlMsgs) != 1 {
		return nil, fmt.Errorf("expected 1 SocketControlMessage; got scms = %#v", controlMsgs)
	}

	controlMsg := controlMsgs[0]
	controlFiles, err := syscall.ParseUnixRights(&controlMsg)
	if err != nil {
		return nil, fmt.Errorf("syscall.ParseUnixRights: %v", err)
	}
	if len(controlFiles) != 1 {
		return nil, fmt.Errorf("wanted 1 fd; got %#v", controlFiles)
	}
	return os.NewFile(uintptr(controlFiles[0]), "/dev/fuse"), nil
}
