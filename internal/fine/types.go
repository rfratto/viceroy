package fine

import (
	"fmt"
	"os"
	"time"
)

// TODO(rfratto): read about whiteouts, since it's relevant to this while
// project (overlay/union FSs)

var (
	// MinVersion supported by the package. Earlier versions may be supported, but
	// are not tested to work.
	MinVersion = Version{Major: 7, Minor: 31}

	// RootNode represents the root filesystem. It always has inode ID 1.
	RootNode Node = Node(1)
)

// Version of the protocol.
type Version struct{ Major, Minor uint32 }

// String implements fmt.Stringer.
func (v Version) String() string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

// ID types. FUSE has a collection of handles that are used during the lifetime
// of a connection.
type (
	// Node is an ID representing a file. 0 is never a valid reference. 1 will
	// always refer to the root filesystem of the driver, and is always assumed
	// to exist by both sides of the peer connection.
	Node uint64

	// Handle is a specific handle for a Node. Handles must have unique IDs for
	// the lifetime of the handle. Handle IDs may be reassigned to other Nodes
	// once the handle is released.
	Handle uint64

	// LockOwner is an opaque ID that references an owner of a file lock.
	LockOwner uint64
)

// Common data types. Common data types represent entities in a filesystem and
// are communicated over the protocol as part of messages.
type (
	// RequestHeader is present in every request.
	RequestHeader struct {
		Op        Op     // Op representing the request.
		RequestID uint64 // Response must match this value.
		Node      Node   // Node the request is for.
		UID       uint32 // UID of requesting user.
		GID       uint32 // GID of requesting user.
		PID       uint32 // PID of requesting user.
	}

	// ResponseHeader is present in every response.
	ResponseHeader struct {
		Op        Op     // Op representing the response. Must match Op from request.
		RequestID uint64 // Request for which this response applies to.
		Error     Error
	}

	// Entry is a description of a file.
	Entry struct {
		Node       Node          // Node ID.
		Generation uint64        // Generation of Node. Increase whenever Node value wraps around to 0.
		EntryTTL   time.Duration // Cache validility of this Node.
		AttribTTL  time.Duration // Cache validility of this Node's attributes.
		Attrib     Attrib        // Attributes for the Node.
	}

	// Attrib are the set of attributes for a Node.
	Attrib struct {
		Inode      uint64      // Real inode number.
		Size       uint64      // Size in bytes.
		Blocks     uint64      // Size in blocks (512-byte units).
		LastAccess time.Time   // Last time file was accessed.
		LastModify time.Time   // Last time contents were modified
		LastChange time.Time   // Last time inode was updated.
		Mode       os.FileMode // File permissions.
		HardLinks  uint32      // Number of hard links to the file (usually 1)
		UID        uint32      // Owner UID
		GID        uint32      // Owner GID
		DeviceID   uint32      // Device ID (if special file)
		BlockSize  uint32      // Block size for filesystem i/O
	}

	// DirEntry is a directory entry returned during ReadDir.
	DirEntry struct {
		Inode uint64
		Type  EntryType
		Name  string
	}

	// DirPlusEntry is returned as part of a ReadDirPlus operation.
	DirPlusEntry struct {
		Entry    Entry
		DirEntry DirEntry
	}

	// Lock is a POSIX advisory lock.
	Lock struct {
		Start uint64   // Absolute starting byte offset to lock.
		End   uint64   // Last byte offset to lock. Zero means to lock entire file past start.
		Type  LockType // Type of lock.
		PID   uint32   // PID of holding process
	}

	BatchForgetItem struct {
		Node       Node
		NumLookups uint64
	}
)

// Enum types.
type (
	// EntryType specifies the type of a file in a directory.
	EntryType uint32

	// LockType indicates the type of file lock.
	LockType uint32
)

// Enum values.
const (
	EntryUnknown    EntryType = 0x0 // Entry type isn't known
	EntryPipe       EntryType = 0x1 // Entry is a named FIFO pipe
	EntryCharacter  EntryType = 0x2 // Entry is a character device
	EntryDirectory  EntryType = 0x4 // Entry is another directory
	EntryBlock      EntryType = 0x6 // Entry is a block device
	EntryRegular    EntryType = 0x8 // Entry is a regular file
	EntryLink       EntryType = 0xa // Entry is a symbolic link
	EntryUnixSocket EntryType = 0xc // Entry is a UNIX domain socket
	EntryWhiteout   EntryType = 0xe // Entry is a BSD whiteout

	LockTypeRead   LockType = 0x0 // Read lock
	LockTypeWrite  LockType = 0x1 // Write lock
	LockTypeUnlock LockType = 0x2 // Used to release locks
)

// Flag types. Every flag type here is a bitmask of options.
type (
	// GetAttribFlags is a bitmask of flags for GetAttribRequest.
	GetAttribFlags uint32
	// AttribMask is used when setting file attributes to mark which fields from
	// the request can be used.
	AttribMask uint32
	// Flags used for interacting with a node.
	FileFlags uint32
	// Flags returned for an opened file.
	OpenedFlags uint32
	// ReadFlags are used to customize a ReadRequest.
	ReadFlags uint32
	// WriteFlags are used to customize a WriteRequest.
	WriteFlags uint32
	// ReleaseFlags customize a lock release.
	ReleaseFlags uint32
	// SyncFlags controls a file sync.
	SyncFlags uint32
	// ExtendedAttribFlags controls setting an extended attribute.
	ExtendedAttribFlags uint32
	// Flags used during an init.
	InitFlags uint32
	// LockFlags control how a lock is created.
	LockFlags uint32
	// DeviceControlFlags change the behavior of ioctl.
	DeviceControlFlags uint32
	// PollFlags modify the behavior of a poll.
	PollFlags uint32
	// PollEvents are poll flags for events to request.
	PollEvents uint32
	// RenameFlags is used during an extended rename to control its behavior.
	RenameFlags uint32
	// CUSEInitFlags are used during a CUSE init.
	CUSEInitFlags uint32
)

// Monolith of available flag options.
const (
	// GetAttribFlagHandle request attributes for a handle instead of the node.
	GetAttribFlagHandle GetAttribFlags = (1 << 0)

	AttribMaskMode          AttribMask = 1 << 0  // The Mode field can be used
	AttribMaskUID           AttribMask = 1 << 1  // The UID field can be used
	AttribMaskGID           AttribMask = 1 << 2  // The GID field can be used
	AttribMaskSize          AttribMask = 1 << 3  // The Size field can be used
	AttribMaskLastAccess    AttribMask = 1 << 4  // The LastAccess field can be used
	AttribMaskLastModify    AttribMask = 1 << 5  // The LastModify field can be used
	AttribMaskFileHandle    AttribMask = 1 << 6  // The FileHandle field can be used
	AttribMaskLastAccessNow AttribMask = 1 << 7  // Update LastAccess to the current time
	AttribMaskLastModifyNow AttribMask = 1 << 8  // Update LastModify to the current time
	AttribMaskLockOwner     AttribMask = 1 << 9  // The LockOwner field can be used
	AttribMaskLastChange    AttribMask = 1 << 10 // The LastChange field can be used

	OpenReadOnly  FileFlags = 0x0 // Open the file for reading.
	OpenWriteOnly FileFlags = 0x1 // Open the file for writing.
	OpenReadWrite FileFlags = 0x2 // Open the file for reading and writing.
	OpenAccesMode FileFlags = 0x3 // Open the file to get access mode bits.

	OpenCreate    FileFlags = 0x40     // Create the file if it doesn't exist.
	OpenExclusive FileFlags = 0x80     // Open the file with an exclusive lock.
	OpenTruncate  FileFlags = 0x200    // Truncate file contents before opening for writing
	OpenAppend    FileFlags = 0x400    // Open with the file seeked to the end.
	OpenNonblock  FileFlags = 0x800    // Enable non-blocking IO against the open file.
	OpenDirectory FileFlags = 0x10000  // Open the file as a directory.
	OpenSync      FileFlags = 0x101000 // Enable synchronous writes

	OpenedDirectIO    OpenedFlags = 1 << 0 // Page cache should be bypassed when writing
	OpenedKeepCache   OpenedFlags = 1 << 1 // Existing page cache should be kept intact
	OpenedNonSeekable OpenedFlags = 1 << 2 // File does not support seeking
	OpenedCacheDir    OpenedFlags = 1 << 3 // Allow caching directory
	OpenedStream      OpenedFlags = 1 << 4 // The file is stream-like (it has no position)

	ReadLockOwner ReadFlags = 1 << 1 // Use LockOwner to check exclusive lock

	WriteCache     WriteFlags = 1 << 0 // Delayed write from cache
	WriteLockOwner WriteFlags = 1 << 1 // Lock owner field may be used for validating lock
	WriteKillPriv  WriteFlags = 1 << 2 // Kill suid and gid bits

	ReleaseFlush  ReleaseFlags = 1 << 0 // Flush the file after releasing
	ReleaseUnlock ReleaseFlags = 1 << 1 // Remove the lock after releasing

	SyncDataOnly SyncFlags = 1 << 0 // Only sync data, not file metadata

	ExtendedAttribCreate ExtendedAttribFlags = 0x1 // Fail if the attrib already exists
	ExtendedAttribReplce ExtendedAttribFlags = 0x2 // Fail if the attrib doesn't already exist

	InitAsyncRead               InitFlags = 1 << 0  // Use asynchronous read requests
	InitPOSIXLocks              InitFlags = 1 << 1  // Use POSIX file locks
	InitFileOps                 InitFlags = 1 << 2  // Kernel sends a file handle
	InitAtomicTruncate          InitFlags = 1 << 3  // OpenTruncate is handled in the filesystem
	InitExportSupport           InitFlags = 1 << 4  // Filesystem can handle "." and ".."
	InitBigWrites               InitFlags = 1 << 5  // Filesystem can handle writes larger than 4K
	InitNoUmask                 InitFlags = 1 << 6  // Don't apply Umask to file mode on create operations
	InitSpliceWrite             InitFlags = 1 << 7  // Kernel supports splice write on the device
	InitSpliceMove              InitFlags = 1 << 8  // Kernel supports splice move on the device
	InitSpliceRead              InitFlags = 1 << 9  // Kernel supports splice read on the device
	InitBSDLocks                InitFlags = 1 << 10 // Use BSD style locks
	InitDirIoctl                InitFlags = 1 << 11 // Kernel supports running ioctl on directories
	InitAutoInvalidateCache     InitFlags = 1 << 12 // Automatically invalidate cached pages
	InitUseReadDirPlus          InitFlags = 1 << 13 // Use ReadDirPlus instead of ReadDir
	InitAdaptiveReadDirPlus     InitFlags = 1 << 14 // Adaptive ReadDirPlus
	InitAsyncDIO                InitFlags = 1 << 15 // Asynchronous direct I/O submission
	InitWritebackCache          InitFlags = 1 << 16 // Use writeback cache for buffered writes
	InitZeroOpenSupport         InitFlags = 1 << 17 // Kernel supports zero-message opens
	InitParallelDirOps          InitFlags = 1 << 18 // Allow parallel operations on directories
	InitHandleKillpriv          InitFlags = 1 << 19 // Filesystem will kill suid/sgid/cap on write/chown/trunc
	InitACLSupportPOSIX         InitFlags = 1 << 20 // Filesystem has support for POSIX ACLs
	InitAbortError              InitFlags = 1 << 21 // Reading the device after abort will return ErrorAborted
	InitMaxPages                InitFlags = 1 << 22 // Set max pages on the init response
	InitCacheSymlinks           InitFlags = 1 << 23 // Cache respones for symblic links
	InitZeroOpenDirSupport      InitFlags = 1 << 24 // Kernel supports zero-message open directory
	InitExplicitCacheInvalidate InitFlags = 1 << 25 // Only invalidated caches after explicitly requested
	InitMapAlignment            InitFlags = 1 << 26 // Read MapAlignment field from init response

	LockFlock LockFlags = 1 << 0 // BSD-style lock

	DeviceControlCompat       DeviceControlFlags = 1 << 0 // 32-bit compatible ioctl
	DeviceControlUnrestricted DeviceControlFlags = 1 << 1 // Allow retries
	DeviceControlRetry        DeviceControlFlags = 1 << 2 // Request should be retried
	DeviceControlBitSize32    DeviceControlFlags = 1 << 3 // Use 32bit ioctl
	DeviceControlDirectory    DeviceControlFlags = 1 << 4 // ioctl on a directory
	DeviceControlX32Compat    DeviceControlFlags = 1 << 5 // x32 ioctl on 64bit machine (64bit time_t)

	PollScheduleNotify PollFlags = 1 << 0 // Request poll notify

	PollEventsIn             PollEvents = 0x0001 // Data is available for reading
	PollEventsPriority       PollEvents = 0x0002 // There is an exceptional condition on the file
	PollEventsOut            PollEvents = 0x0004 // Writing is now possible
	PollEventsError          PollEvents = 0x0008 // Some returned error condition
	PollEventsHangup         PollEvents = 0x0010 // Peer closed connection
	PollEventsInvalid        PollEvents = 0x0020 // Invalid request
	PollEventsReadNormal     PollEvents = 0x0040 // There is data to read
	PollEventsReadOutOfBand  PollEvents = 0x0080 // Out of band data can be read
	PollEventsWriteNormal    PollEvents = 0x0100 // Writing is now possible
	PollEventsWriteOutOfBand PollEvents = 0x0200 // Out of band data can be written
	PollEventsReadHangup     PollEvents = 0x2000 // Stream socket peer closed connection
	DefaultPollMask          PollEvents = PollEventsIn | PollEventsOut | PollEventsReadNormal | PollEventsWriteNormal

	RenameAtomic    RenameFlags = 0 // Atomically exchange the old and new file
	RenameNoReplace RenameFlags = 0 // Don't overwrite NewName if it already exists
	RenameWhiteout  RenameFlags = 0 // Create a whiteout object at the source.

	CUSEUnrestrictedIoctl CUSEInitFlags = 1 << 0 // Use unrestricted ioctl
)
