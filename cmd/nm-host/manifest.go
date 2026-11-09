// Ported from Mister Download Manager, crates/nmhost/src/main.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/vaisah/internal/browser"
	"github.com/vaisah/internal/ipc"
)

// chromiumManifest is the manifest Chromium browsers read: the host
// (program) may serve our extension's origins only.
func chromiumManifest(program string) map[string]any {
	return map[string]any{
		"name": browser.HostName, "description": ipc.AppName, "path": program, "type": "stdio",
		"allowed_origins": browser.AllowedOrigins(),
	}
}

// firefoxManifest is Firefox's: the same host, allowed for our add-on ID.
func firefoxManifest(program string) map[string]any {
	return map[string]any{
		"name": browser.HostName, "description": ipc.AppName, "path": program, "type": "stdio",
		"allowed_extensions": []string{browser.FirefoxID},
	}
}

func manifestFor(f browser.Family, program string) map[string]any {
	if f == browser.Gecko {
		return firefoxManifest(program)
	}
	return chromiumManifest(program)
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// portableRefused: a portable copy writes nothing outside its folder.
func portableRefused() error {
	if _, ok := ipc.PortableDir(); ok {
		return errors.New("portable mode: browser integration is not registered (nothing is written " +
			"outside its folder); install Yellow Flash to use it")
	}
	return nil
}

// packagedHost is where Linux packages install this program, which their
// manifests name.
const packagedHost = "/usr/bin/" + programName

// thisProgram is this program, which the manifests name.
func thisProgram() (string, error) { return os.Executable() }
