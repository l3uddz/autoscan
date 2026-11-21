//go:build !unix

package inotify

type fileID struct {
	dev uint64
	ino uint64
}

func statFileID(string) (fileID, bool) {
	return fileID{}, false
}
