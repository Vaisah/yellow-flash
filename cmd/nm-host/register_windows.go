// Ported from Mister Download Manager, crates/nmhost/src/main.rs and win32.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/vaisah/internal/browser"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// registryKeys are where each Chromium browser looks for native messaging
// hosts, under HKCU (one user) or HKLM (everyone). Chromium-branded builds
// (Chromium, Helium, Thorium, Brave) read Chromium's first, then Chrome's;
// Brave's own key is kept for builds that read it.
var registryKeys = []string{
	`Software\Google\Chrome\NativeMessagingHosts`,
	`Software\Chromium\NativeMessagingHosts`,
	`Software\Microsoft\Edge\NativeMessagingHosts`,
	`Software\BraveSoftware\Brave-Browser\NativeMessagingHosts`,
}

// firefoxKey is where Firefox and its forks look.
const firefoxKey = `Software\Mozilla\NativeMessagingHosts`

// chromeExternalKey: an entry naming the store's extension makes Chrome
// offer it at its next start. HKLM's is read from the 32-bit view on
// 64-bit Windows.
const chromeExternalKey = `Software\Google\Chrome\Extensions`

// edgeExternalKey is Edge's: an entry naming the Edge Add-ons extension
// makes Edge offer it at its next start.
const edgeExternalKey = `Software\Microsoft\Edge\Extensions`

func rootKey(allUsers bool) registry.Key {
	if allUsers {
		return registry.LOCAL_MACHINE
	}
	return registry.CURRENT_USER
}

func setValue(root registry.Key, path, name, value string, access uint32) error {
	k, _, err := registry.CreateKey(root, path, registry.SET_VALUE|access)
	if err != nil {
		return fmt.Errorf(`%s: %w`, path, err)
	}
	defer k.Close()
	return k.SetStringValue(name, value)
}

var procRegDeleteKeyEx = windows.NewLazySystemDLL("advapi32.dll").NewProc("RegDeleteKeyExW")

// deleteKey removes path (a key without subkeys) in the given view.
func deleteKey(root registry.Key, path string, access uint32) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil || procRegDeleteKeyEx.Find() != nil {
		return
	}
	_, _, _ = procRegDeleteKeyEx.Call(uintptr(root), uintptr(unsafe.Pointer(p)), uintptr(access), 0)
}

// register writes the manifests beside this exe and the registry entries
// naming them, per user (HKCU) or for everyone (HKLM).
func register(allUsers bool) error {
	if err := portableRefused(); err != nil {
		return err
	}
	program, err := thisProgram()
	if err != nil {
		return err
	}
	dir := filepath.Dir(program)
	root := rootKey(allUsers)
	path := filepath.Join(dir, browser.HostManifest)
	if err := writeJSON(path, chromiumManifest(program)); err != nil {
		return err
	}
	for _, key := range registryKeys {
		if err := setValue(root, key+`\`+browser.HostName, "", path, 0); err != nil {
			return err
		}
	}
	ffPath := filepath.Join(dir, browser.HostName+".firefox.json")
	if err := writeJSON(ffPath, firefoxManifest(program)); err != nil {
		return err
	}
	if err := setValue(root, firefoxKey+`\`+browser.HostName, "", ffPath, 0); err != nil {
		return err
	}
	// Chrome offers the store's extension once it is listed (never a
	// placeholder ID).
	if id, ok := browser.StoreIDValid(); ok {
		if err := setValue(root, chromeExternalKey+`\`+id, "update_url", browser.StoreUpdateURL, registry.WOW64_32KEY); err != nil {
			return err
		}
	}
	// Edge the same, once the extension is in Edge Add-ons.
	if id, ok := browser.EdgeIDValid(); ok {
		if err := setValue(root, edgeExternalKey+`\`+id, "update_url", browser.EdgeAddonsUpdateURL, registry.WOW64_32KEY); err != nil {
			return err
		}
	}
	fmt.Printf("registered %s for Chrome, Edge, Brave, Helium and the other Chromium browsers (%s), "+
		"and for Firefox, Zen and its other forks (%s)\n", browser.HostName, path, ffPath)
	return nil
}

func unregister(allUsers bool) error {
	root := rootKey(allUsers)
	for _, key := range append(registryKeys, firefoxKey) {
		deleteKey(root, key+`\`+browser.HostName, 0)
	}
	if id, ok := browser.StoreIDValid(); ok {
		deleteKey(root, chromeExternalKey+`\`+id, registry.WOW64_32KEY)
	}
	if id, ok := browser.EdgeIDValid(); ok {
		deleteKey(root, edgeExternalKey+`\`+id, registry.WOW64_32KEY)
	}
	if program, err := thisProgram(); err == nil {
		dir := filepath.Dir(program)
		_ = os.Remove(filepath.Join(dir, browser.HostManifest))
		_ = os.Remove(filepath.Join(dir, browser.HostName+".firefox.json"))
	}
	fmt.Printf("unregistered %s\n", browser.HostName)
	return nil
}

func packageManifests(string) error {
	return errors.New("--package-manifests: the system-wide manifests are Linux's")
}

const (
	detachedProcess        = 0x00000008
	createNewProcessGroup  = 0x00000200
	createBreakawayFromJob = 0x01000000
)

// startOutsideJob starts app detached and out of the browser's job object;
// leaving the job is refused when the job doesn't allow it: then without.
func startOutsideJob(app string) (*exec.Cmd, error) {
	var err error
	for _, flags := range []uint32{
		detachedProcess | createNewProcessGroup | createBreakawayFromJob,
		detachedProcess | createNewProcessGroup,
	} {
		c := exec.Command(app)
		c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
		if err = c.Start(); err == nil {
			return c, nil
		}
	}
	return nil, err
}
