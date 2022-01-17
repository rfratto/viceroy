package fine

import (
	"os"
	"time"
)

// Protocol types. Each type here is used as part of the request or response
// for a specific operation. Protocol types have associated opcodes, which can
// be retrieved by passing the types to GetOp.
type (
	LookupRequest struct {
		Name string
	}
	EntryResponse struct {
		Entry Entry
	}

	ForgetRequest struct {
		NumLookups uint64
	}

	GetattrRequest struct {
		Flags  GetAttribFlags
		Handle Handle
	}
	SetattrRequest struct {
		UpdateMask AttribMask  // Mask indicating which fields to use for the update.
		Handle     Handle      // Handle to set attributes for.
		Size       uint64      // File size.
		LockOwner  LockOwner   // Owner of a lock.
		LastAccess time.Time   // Last time file was accessed.
		LastModify time.Time   // Last time file was modified.
		LastChange time.Time   // Last time file was updated.
		Mode       os.FileMode // File permissions.
		UID        uint32      // Owner UID
		GID        uint32      // Owner GID
	}
	AttrResponse struct {
		TTL    time.Duration // Cache validility of the attributes.
		Attrib Attrib        // Attribute data
	}

	ReadlinkResponse struct {
		Contents []byte // Contents of the link, up to the page size.
	}

	SymlinkRequest struct {
		Source   string // File being created
		LinkName string // File being linked to
	}

	MknodRequest struct {
		Mode     os.FileMode // Permissions for the file
		DeviceID uint32      // Device ID for the special file
		Umask    os.FileMode // Umask of the request
		Name     string      // Name of the file
	}

	MkdirRequest struct {
		Mode  os.FileMode
		Umask os.FileMode
		Name  string
	}

	UnlinkRequest struct {
		Name string
	}

	RmdirRequest struct {
		Name string
	}

	RenameRequest struct {
		NewDir           Node
		OldName, NewName string
	}

	LinkRequest struct {
		OldNode Node
		NewName string
	}

	OpenRequest struct {
		Flags FileFlags
	}
	OpenedResponse struct {
		Handle      Handle
		OpenedFlags OpenedFlags
	}

	ReadRequest struct {
		Handle    Handle
		Offset    uint64
		Size      uint32
		Flags     ReadFlags
		LockOwner LockOwner
		FileFlags FileFlags
	}
	ReadResponse struct {
		Data []byte
	}

	WriteRequest struct {
		Handle    Handle     // Handle to write to
		Offset    uint64     // Offset in the handle to write
		Data      []byte     // Data to write
		Flags     WriteFlags // Flags for writing
		LockOwner LockOwner  // Owner of the write lock, if one exists.
		FileFlags FileFlags  // Permissions for writing
	}
	WriteResponse struct {
		Written uint32 // Written bytes
	}

	ReleaseRequest struct {
		Handle    Handle
		Flags     ReleaseFlags
		FileFlags FileFlags
		LockOwner LockOwner
	}

	FsyncRequest struct {
		Handle Handle
		Flags  SyncFlags
	}

	FlushRequest struct {
		Handle    Handle
		LockOwner LockOwner
	}

	InitRequest struct {
		LatestVersion Version   // LatestVersion supported by the driver
		MaxReadahead  uint32    // Length of data that can be prefetched
		Flags         InitFlags // Flags for the init
	}
	InitResponse struct {
		EarliestVersion     Version   // Earliest version supported by the filesystem
		MaxReadahead        uint32    // Length of data that can be prefetched
		Flags               InitFlags // Response init flags
		MaxBackground       uint16
		CongestionThreshold uint16
		MaxWrite            uint32
		TimeGran            uint32
		MaxPages            uint16
		MapAlignment        uint16
	}

	ReaddirResponse struct {
		Entries []DirEntry
	}

	AccessRequest struct {
		Mask os.FileMode // Validate access for mask
	}

	CreateRequest struct {
		Flags FileFlags   // Flags for creation
		Mode  os.FileMode // File mode
		Umask os.FileMode // Umask for file
		Name  string      // Name of file to create
	}
	CreateResponse struct {
		Handle      Handle      // Handle to newly created node
		OpenedFlags OpenedFlags // Flags used for the create
		Entry       Entry       // Created node entry
	}

	// InterruptRequest interrupts an ongoing request. The interupted request
	// should return with ErrorInterrupted. Handler implementations may ignore
	// the context cancelation that comes from an interrupt.
	InterruptRequest struct {
		RequestID uint64 // Request to interrupt
	}

	BatchForgetRequest struct {
		Items []BatchForgetItem
	}

	LseekRequest struct {
		Handle Handle // Handle to seek in
		Offset uint64 // Offset to seek to, relative to whence
		Whence uint32 // Either seek relative to beginning, current position, or end.
	}
	LseekResponse struct {
		Offset uint64 // New offset in the file
	}
)

//
// Request / Response type implementations
//

func (*LookupRequest) fineRequest()      {}
func (*EntryResponse) fineResponse()     {}
func (*ForgetRequest) fineRequest()      {}
func (*GetattrRequest) fineRequest()     {}
func (*SetattrRequest) fineRequest()     {}
func (*AttrResponse) fineResponse()      {}
func (*ReadlinkResponse) fineResponse()  {}
func (*SymlinkRequest) fineRequest()     {}
func (*MknodRequest) fineRequest()       {}
func (*MkdirRequest) fineRequest()       {}
func (*UnlinkRequest) fineRequest()      {}
func (*RmdirRequest) fineRequest()       {}
func (*RenameRequest) fineRequest()      {}
func (*LinkRequest) fineRequest()        {}
func (*OpenRequest) fineRequest()        {}
func (*OpenedResponse) fineResponse()    {}
func (*ReadRequest) fineRequest()        {}
func (*ReadResponse) fineResponse()      {}
func (*WriteRequest) fineRequest()       {}
func (*WriteResponse) fineResponse()     {}
func (*ReleaseRequest) fineRequest()     {}
func (*FsyncRequest) fineRequest()       {}
func (*FlushRequest) fineRequest()       {}
func (*InitRequest) fineRequest()        {}
func (*InitResponse) fineResponse()      {}
func (*ReaddirResponse) fineResponse()   {}
func (*AccessRequest) fineRequest()      {}
func (*CreateRequest) fineRequest()      {}
func (*CreateResponse) fineResponse()    {}
func (*InterruptRequest) fineRequest()   {}
func (*BatchForgetRequest) fineRequest() {}
func (*LseekRequest) fineRequest()       {}
func (*LseekResponse) fineResponse()     {}
