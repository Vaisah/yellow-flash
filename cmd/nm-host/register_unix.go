// Ported from Mister Download Manager, crates/nmhost/src/main.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"

	"github.com/vaisah/internal/browser"
)

// hostDirs are the folders the browsers here read host manifests from, for
// one user (in the home folder) or for everyone. With host, a user's
// folder is left out for a browser that reads a manifest for everyone
// naming it (a package's).
func hostDirs(allUsers bool, host string) []browser.HostDir {
	home := os.Getenv("HOME")
	config := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(config) {
		config = filepath.Join(home, ".config")
	}
	return browser.HostDirs(home, config, runtime.GOOS == "darwin", allUsers, host)
}

// chromeExternalDir is the folder Chrome reads external extensions from (an
// <id>.json naming the store's update service makes Chrome offer that
// extension), when Chrome is there. macOS has one per user and one for
// everyone; Linux Chrome reads only the system's.
func chromeExternalDir(allUsers bool) (string, bool) {
	home := os.Getenv("HOME")
	var dir, chrome string
	switch {
	case runtime.GOOS == "darwin" && allUsers:
		dir, chrome = "/Library/Application Support/Google/Chrome/External Extensions", "/Applications/Google Chrome.app"
	case runtime.GOOS == "darwin":
		chrome = filepath.Join(home, "Library", "Application Support", "Google", "Chrome")
		dir = filepath.Join(chrome, "External Extensions")
	case allUsers:
		dir, chrome = "/usr/share/google-chrome/extensions", "/opt/google/chrome"
	default:
		return "", false
	}
	if _, err := os.Stat(chrome); err != nil {
		return "", false
	}
	return dir, true
}

// mountRefused: run from inside an AppImage's mount (APPDIR), which is gone
// once the AppImage quits, a manifest would name a program that no longer
// exists. A copy outside it is registered instead.
func mountRefused() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	mount := os.Getenv("APPDIR")
	if mount != "" && (exe == mount || strings.HasPrefix(exe, strings.TrimSuffix(mount, "/")+"/")) {
		return fmt.Errorf("%s is inside the AppImage's mount, which is gone once it quits: register a copy outside it", exe)
	}
	return nil
}

func register(allUsers bool) error {
	if err := portableRefused(); err != nil {
		return err
	}
	if err := mountRefused(); err != nil {
		return err
	}
	program, err := thisProgram()
	if err != nil {
		return err
	}
	dirs := hostDirs(allUsers, program)
	// A browser that reads a package's manifest for everyone, naming this
	// program, needs none in the home folder: one written there before is
	// taken away, so removing the package leaves nothing behind.
	covered := 0
	if !allUsers {
		for _, d := range hostDirs(false, "") {
			if slices.ContainsFunc(dirs, func(x browser.HostDir) bool { return x.Dir == d.Dir }) {
				continue
			}
			covered++
			manifest := filepath.Join(d.Dir, browser.HostManifest)
			if os.Remove(manifest) == nil {
				fmt.Printf("removed %s (the browser reads the one for everyone)\n", manifest)
			}
		}
	}
	if covered > 0 {
		fmt.Printf("registered %s for everyone by the package: %d of this user's browser folders need nothing\n",
			browser.HostName, covered)
	}
	// One browser's folder that can't be written doesn't keep the others
	// from theirs.
	var failed []string
	for _, d := range dirs {
		err := os.MkdirAll(d.Dir, 0o755)
		if err == nil {
			err = writeJSON(filepath.Join(d.Dir, browser.HostManifest), manifestFor(d.Family, program))
		}
		if err != nil {
			failed = append(failed, err.Error())
			continue
		}
		fmt.Printf("registered %s in %s\n", browser.HostName, d.Dir)
	}
	if len(dirs) == 0 && covered == 0 {
		fmt.Printf("registered %s for no browser: none found\n", browser.HostName)
	}
	if len(failed) > 0 {
		return errors.New(strings.Join(failed, "; "))
	}
	if dir, ok := chromeExternalDir(allUsers); ok {
		if id, ok := browser.StoreIDValid(); ok {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			if err := writeJSON(filepath.Join(dir, id+".json"), map[string]string{"external_update_url": browser.StoreUpdateURL}); err != nil {
				return err
			}
			fmt.Printf("offered the extension to Chrome in %s\n", dir)
		}
	}
	return nil
}

func unregister(allUsers bool) error {
	for _, d := range hostDirs(allUsers, "") {
		_ = os.Remove(filepath.Join(d.Dir, browser.HostManifest))
	}
	if dir, ok := chromeExternalDir(allUsers); ok {
		_ = os.Remove(filepath.Join(dir, browser.StoreID+".json"))
	}
	fmt.Printf("unregistered %s\n", browser.HostName)
	return nil
}

// packageManifests writes, for Linux packaging, the manifests for everyone
// in each folder the browsers read them from, under root, naming the
// packaged helper. The package owns them, so removing it removes them.
func packageManifests(root string) error {
	if runtime.GOOS != "linux" {
		return errors.New("--package-manifests: the system-wide manifests are Linux's")
	}
	if root == "" {
		return errors.New("--package-manifests needs a folder")
	}
	for _, s := range browser.LinuxSystemHostDirs {
		dir := filepath.Join(root, strings.TrimPrefix(s.Dir, "/"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		path := filepath.Join(dir, browser.HostManifest)
		if err := writeJSON(path, manifestFor(s.Family, packagedHost)); err != nil {
			return err
		}
		fmt.Println(path)
	}
	return nil
}

// startOutsideJob starts app in a process group of its own, which the
// browser does not end with the host.
func startOutsideJob(app string) (*exec.Cmd, error) {
	c := exec.Command(app)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return c, c.Start()
}
