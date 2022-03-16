//go:build aix || dragonfly || (js && wasm) || linux || solaris

package server

import (
	"io/fs"
	"os"
	"syscall"
	"time"

	"github.com/rfratto/viceroy/internal/fine"
)

func attrFromInfo(n *passthroughNode, fi fs.FileInfo) fine.Attrib {
	attr := fine.Attrib{
		Inode:      n.inode,
		Size:       uint64(fi.Size()),
		Blocks:     uint64(fi.Size() * 512),
		LastModify: fi.ModTime().UTC(),
		Mode:       fi.Mode(),
		BlockSize:  512,
		HardLinks:  1,
	}

	if s, ok := fi.Sys().(*syscall.Stat_t); ok {
		attr.Inode = s.Ino
		attr.Size = uint64(s.Size)
		attr.Blocks = uint64(s.Blocks)
		attr.LastAccess = time.Unix(s.Atim.Sec, s.Atim.Nsec)
		attr.LastModify = time.Unix(s.Mtim.Sec, s.Mtim.Nsec)
		attr.LastChange = time.Unix(s.Ctim.Sec, s.Ctim.Nsec)
		attr.Mode = toNativeMode(uint32(s.Mode))
		attr.HardLinks = uint32(s.Nlink)
		attr.UID = s.Uid
		attr.GID = s.Gid
		attr.RDev = uint32(s.Rdev)
		attr.BlockSize = uint32(s.Blksize)
	}

	return attr
}

func toNativeMode(in uint32) os.FileMode {
	out := os.FileMode(in & 0777)
	switch in & syscall.S_IFMT {
	case syscall.S_IFBLK:
		out |= os.ModeDevice
	case syscall.S_IFCHR:
		out |= os.ModeDevice | os.ModeCharDevice
	case syscall.S_IFDIR:
		out |= os.ModeDir
	case syscall.S_IFIFO:
		out |= os.ModeNamedPipe
	case syscall.S_IFLNK:
		out |= os.ModeSymlink
	case syscall.S_IFREG:
		// nothing to do
	case syscall.S_IFSOCK:
		out |= os.ModeSocket
	case 0:
		out |= os.ModeIrregular
	}
	if in&syscall.S_ISGID != 0 {
		out |= os.ModeSetgid
	}
	if in&syscall.S_ISUID != 0 {
		out |= os.ModeSetuid
	}
	if in&syscall.S_ISVTX != 0 {
		out |= os.ModeSticky
	}
	return out
}
