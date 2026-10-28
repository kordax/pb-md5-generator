package content

import (
	"errors"
	"io"
	"os"
)

// The caller chooses a positive read bound and checks its own inventory limit.
// Always close the opened directory, including a failed or incomplete read.
func readPublicationDirectoryEntries(directory *os.File, count int) ([]os.DirEntry, error) {
	entries, err := directory.ReadDir(count)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return entries, errors.Join(err, directory.Close())
}
