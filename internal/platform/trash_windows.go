//go:build windows

package platform

import (
	"errors"
	"fmt"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procSHFileOperationW = windows.NewLazySystemDLL("shell32.dll").NewProc("SHFileOperationW")

// shFileOpStruct is SHFILEOPSTRUCTW (natural alignment on 64-bit Windows).
type shFileOpStruct struct {
	hwnd                  uintptr
	wFunc                 uint32
	pFrom                 *uint16
	pTo                   *uint16
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     *uint16
}

const (
	foDelete          = 3
	fofSilent         = 0x0004
	fofNoConfirmation = 0x0010
	fofAllowUndo      = 0x0040 // to the Recycle Bin
	fofNoErrorUI      = 0x0400
)

func trash(files []string) error {
	// pFrom: the paths, each ended by a NUL, the list by another.
	var from []uint16
	for _, f := range files {
		abs, err := filepath.Abs(f)
		if err != nil {
			return err
		}
		u, err := windows.UTF16FromString(abs)
		if err != nil {
			return err
		}
		from = append(from, u...)
	}
	from = append(from, 0)
	op := shFileOpStruct{wFunc: foDelete, pFrom: &from[0],
		fFlags: fofAllowUndo | fofNoConfirmation | fofSilent | fofNoErrorUI}
	if r, _, _ := procSHFileOperationW.Call(uintptr(unsafe.Pointer(&op))); r != 0 {
		return fmt.Errorf("the Recycle Bin refused it (error %#x)", r)
	}
	if op.fAnyOperationsAborted != 0 {
		return errors.New("moving it to the Recycle Bin was stopped")
	}
	return nil
}
