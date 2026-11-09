//go:build !windows && !linux

package platform

import "os"

// No probe here: the disk check is skipped.
func volumeOf(string) (Volume, bool) { return Volume{}, false }

func setSparse(*os.File, bool) bool { return false }
