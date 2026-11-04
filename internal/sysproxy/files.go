package sysproxy

import (
	"errors"
	"io/fs"
	"os"
)

// removeFile deletes path. A file that is already gone is not an error.
func removeFile(path string) error {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
