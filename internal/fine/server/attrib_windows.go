package server

func attrFromInfo(n *passthroughNode, fi fs.FileInfo) fine.Attrib {
	return fine.Attrib{
		Inode:      n.inode,
		Size:       uint64(fi.Size()),
		Blocks:     uint64(fi.Size() * 512),
		LastModify: fi.ModTime().UTC(),
		Mode:       fi.Mode(),
		BlockSize:  512,
		HardLinks:  0,
	}
}
