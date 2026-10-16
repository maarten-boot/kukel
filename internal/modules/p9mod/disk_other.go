//go:build !no9p && !unix

package p9mod

import "os"

// fileID: without inode numbers, diskPath gives the qid path from the path.
func fileID(fi os.FileInfo) (uint64, bool) { return 0, false }
