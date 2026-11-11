//go:build linux

package platform

import "golang.org/x/sys/unix"

func markFromInternet(path, from, referrer string) {
	if from != "" {
		_ = unix.Setxattr(path, "user.xdg.origin.url", []byte(from), 0)
	}
	if referrer != "" {
		_ = unix.Setxattr(path, "user.xdg.referrer.url", []byte(referrer), 0)
	}
}
