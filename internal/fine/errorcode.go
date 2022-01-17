package fine

import (
	"strconv"
)

// Error is a FUSE error code. FUSE accepts POSIX error codes that are inverted
// to be negative (i.e., -syscall.ENOTSUP).
//
// The most common codes are re-defined here for cross-platform compatbility.
type Error int32

// Common error codes. Custom error codes may be used as long as their value is
// negative and represents a standard POSIX error (i.e., -syscall.ENOTSUP).
// Error codes must be understanable by Linux and necessarily have the same
// representation on the host machine.
const (
	ErrorNotPermitted     = Error(-0x01) // EPERM
	ErrorNotExist         = Error(-0x02) // ENOENT
	ErrorInterrupted      = Error(-0x04) // EINTR
	ErrorIO               = Error(-0x05) // EIO
	ErrorTooManyArguments = Error(-0x07) // E2BIG
	ErrorUnavailable      = Error(-0x0b) // EAGAIN
	ErrorNoMemory         = Error(-0x0c) // ENOMEM
	ErrorUnauthorized     = Error(-0x0d) // EACCES
	ErrorExists           = Error(-0x11) // EEXIST
	ErrorBadCrossLink     = Error(-0x12) // EXDEV
	ErrorNoDevice         = Error(-0x13) // ENODEV
	ErrorInvalid          = Error(-0x16) // EINVAL
	ErrorBadIoctl         = Error(-0x19) // ENOTTY
	ErrorNoLock           = Error(-0x25) // ENOLCK
	ErrorUnimplemented    = Error(-0x26) // ENOSYS
	ErrorAborted          = Error(-0x67) // ECONNABORTED
	ErrorStale            = Error(-0x74) // ESTALE
)

// Error description table
var errorDescriptions = map[Error]string{
	ErrorNotPermitted:     "operation not permitted",
	ErrorNotExist:         "no such file or directory",
	ErrorInterrupted:      "interrupted system call",
	ErrorIO:               "input/output error",
	ErrorTooManyArguments: "argument list too long",
	ErrorUnavailable:      "resource temporarily unavailable",
	ErrorNoMemory:         "cannot allocate memory",
	ErrorUnauthorized:     "permission denied",
	ErrorExists:           "file exists",
	ErrorBadCrossLink:     "invalid cross-device link",
	ErrorNoDevice:         "no such device",
	ErrorInvalid:          "invalid argument",
	ErrorBadIoctl:         "inappropriate ioctl for device",
	ErrorNoLock:           "no locks available",
	ErrorUnimplemented:    "function not implemented",
	ErrorAborted:          "software caused connection abort",
	ErrorStale:            "stale file handle",
}

// Error prints the description of the error.
func (e Error) Error() string {
	desc := errorDescriptions[e]
	if desc != "" {
		return desc
	}
	return "FUSE errno " + strconv.Itoa(int(e))
}
