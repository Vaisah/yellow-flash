//go:build !windows && !linux

package platform

func trash([]string) error { return errNoTrash }
