//go:build unix

package inotify

import (
	"os"
	"syscall"
)

type fileID struct {
	dev uint64
	ino uint64
}

func statFileID(path string) (fileID, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return fileID{}, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileID{}, false
	}
	return fileID{dev: uint64(st.Dev), ino: uint64(st.Ino)}, true
}
