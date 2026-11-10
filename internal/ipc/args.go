// Ported from Mister Download Manager, crates/ipc/src/lib.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package ipc

import "strings"

// StartupArg is the argument every launch-at-login entry passes: the app
// starts without bringing its window to the front.
const StartupArg = "/startup"

// ParseArgs parses IDM's command line:
//
//	/d URL [/p dir] [/f name] [/a] [/n] [/q] [/s]
//
// plus /exit, /startup and /show <window>. Flags are case-insensitive and
// may start with "-" or "--" instead of "/". A bare magnet link or .torrent
// path (what a file association passes) is taken as the URL. ok is false
// when there is nothing to do.
func ParseArgs(args []string) (cmd Command, ok bool) {
	next := func(i *int) string {
		if *i+1 < len(args) {
			*i++
			return args[*i]
		}
		return ""
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch strings.ToLower(strings.TrimLeft(a, "/-")) {
		case "d":
			cmd.URL = next(&i)
		case "p":
			cmd.Dir = next(&i)
		case "f":
			cmd.Name = next(&i)
		case "a":
			cmd.Later = true
		case "n":
			cmd.Silent = true
		case "s":
			cmd.StartQueue = true
		case "q", "h":
			// IDM's "quit after" and "hang up": accepted, nothing to do.
		case "exit":
			cmd.Exit = true
		case "startup":
			cmd.Startup = true
		case "show":
			cmd.Show = strings.ToLower(next(&i))
		default:
			if len(a) >= 7 && strings.EqualFold(a[:7], "magnet:") ||
				strings.HasSuffix(strings.ToLower(a), ".torrent") {
				cmd.URL = a
			} else {
				continue
			}
		}
		ok = true
	}
	return cmd, ok
}
