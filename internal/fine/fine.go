// Package fine implements FUSE with network-layer support. FINE stands for
// "FIlesystem over NEtwork." It's not a perfect translation of FUSE, but
// it's... FINE.
//
// fine can be used with any kind of transport. Supported transports are
// the Linux kernel (via `/dev/fuse`) or gRPC. See the fuse and finegrpc
// packages respectively. Non-Linux kernel transports (BSD, macOS) are not
// supported.
//
// fine is a subset of FUSE, though most basic operations are available.
//
// fine was initially written against FUSE 7.31.
package fine

// Request is used for protocol request messages which are sent by a kernel to
// the filesystem driver.
type Request interface {
	fineRequest()
}

// Response is used for protocol response message types which are sent from the
// filesystem driver after processing a request.
type Response interface {
	fineResponse()
}

// Transports are used to transmit FINE protocol messages. See subpackages for
// available transports.
type Transport interface {
	// RecvRequest will get the next request from the other side of the
	// connection. There will always be a request header, but some operations may
	// have empty (nil) requests.
	RecvRequest() (RequestHeader, Request, error)

	// SendResponse sends r to the other side of the connection. There must
	// always be a response header, but some operations do not have responses.
	SendResponse(h ResponseHeader, r Response) error

	// Close the connection.
	Close() error
}
