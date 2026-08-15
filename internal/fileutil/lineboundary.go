package fileutil

import "os"

// EnsureTrailingNewline appends a newline to a non-empty line-oriented file
// whose last byte is not already '\n', so a line torn by a crash mid-write can
// never fuse with the next append. An empty file is left alone — nothing is
// torn yet. f must be opened O_RDWR|O_APPEND (the tail check reads a byte);
// callers that share the file across processes must serialize appends with a
// lock (filelock) or write under an exclusive root.
func EnsureTrailingNewline(f *os.File) error {
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return err
	}
	var tail [1]byte
	if _, err := f.ReadAt(tail[:], st.Size()-1); err != nil {
		return err
	}
	if tail[0] == '\n' {
		return nil
	}
	_, err = f.Write([]byte{'\n'})
	return err
}
