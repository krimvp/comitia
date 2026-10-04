package comitia

import (
	"errors"
	"fmt"
	"os"
)

// SQLite derives newly created WAL/SHM permissions from the database file.
// Never silently repair existing files: they may already have disclosed data.
func privateSQLiteFiles(path string) error {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		name := path + suffix
		info, err := os.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			if suffix != "" {
				continue
			}
			file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
			if errors.Is(err, os.ErrExist) {
				info, err = os.Lstat(name)
			} else if err == nil {
				if err = file.Close(); err != nil {
					return err
				}
				continue
			} else {
				return err
			}
			if err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("%w: sqlite file must be regular and private (0600): %s", ErrInvalid, name)
		}
	}
	return nil
}
