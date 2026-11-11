// Ported from Mister Download Manager, crates/mcp/src/main.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

// Command yellowflash-mcp is Yellow Flash's MCP server. An AI assistant
// starts it and speaks MCP with it on stdin and stdout; it hands what the
// assistant asks to the running app through the app's channel (ipc.json in
// the data folder; YELLOWFLASH_DATA_DIR overrides where that is).
package main

import (
	"fmt"
	"os"

	"github.com/vaisah/internal/ipc"
)

func main() {
	// Started by hand in a terminal: say what it is instead of waiting
	// silently.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-V":
			fmt.Println("yellowflash-mcp " + ipc.AppVersion)
		default:
			fmt.Println("yellowflash-mcp: Yellow Flash's MCP server, for AI assistants. The assistant starts it " +
				"(no arguments) and talks to it on stdin and stdout.")
		}
		return
	}
	if err := NewServer(appAsk, ipc.AppVersion).Serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// appAsk asks the running app found in the data folder.
func appAsk(cmd ipc.Command) (string, error) {
	dir, err := ipc.DataDir()
	if err != nil {
		return "", err
	}
	return ipc.Request(dir, cmd)
}
