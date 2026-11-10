// Ported from Mister Download Manager, crates/cli/src/lib.rs (ask, pair, act)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package main

// The commands that ask the running app, through its channel (ipc.json in
// the data folder).

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/vaisah/internal/ipc"
)

// askApp is the running app's answer to cmd, or why there is none.
func askApp(cmd ipc.Command) (string, error) {
	dir, err := dataDir()
	if err != nil {
		return "", err
	}
	answer, err := ipc.Request(dir, cmd)
	if err != nil {
		return "", fmt.Errorf("Yellow Flash isn't running (%v)", err)
	}
	switch answer {
	case "denied":
		return "", errors.New("Yellow Flash refused the request: its channel file (ipc.json) is from another start; restart the app")
	case "timeout":
		return "", errors.New("Yellow Flash didn't answer in time (a dialog may be open in it)")
	case "gone":
		return "", errors.New("Yellow Flash is quitting")
	case "too-large":
		return "", errors.New("the request is too large")
	}
	return answer, nil
}

// printAnswer asks the app and prints its answer, which is JSON already;
// an {"error"} answer fails the command.
func printAnswer(cmd ipc.Command) error {
	answer, err := askApp(cmd)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, answer)
	if _, e, perr := ipc.ParseAnswer[json.RawMessage](answer); perr == nil && e != nil {
		return errReported
	}
	return nil
}

func cmdStatus(_ context.Context, args []string) error {
	if len(args) != 0 {
		return usageErr("no arguments")
	}
	return printAnswer(ipc.Command{Query: "list"})
}

func cmdAct(action string) func(context.Context, []string) error {
	return func(_ context.Context, args []string) error {
		if len(args) == 0 {
			return usageErr("give at least one id (status shows them)")
		}
		return printAnswer(ipc.Command{Action: action, IDs: args})
	}
}

func cmdAPI(_ context.Context, args []string) error {
	if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
		return usageErr("on or off")
	}
	on := args[0] == "on"
	return printAnswer(ipc.Command{API: &on})
}

func cmdAdd(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	out := fs.String("o", "", "folder to save into (default: the app's download folder)")
	fs.StringVar(out, "output", "", "folder to save into")
	fileName := fs.String("name", "", "file name to save it as")
	later := fs.Bool("later", false, "only add it to a queue, without starting it")
	queue := fs.String("queue", "", "the queue it waits in, with --later (default: the main queue)")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usageErr("one address")
	}
	cmd := ipc.Command{URL: strings.TrimSpace(rest[0]), Name: *fileName, Later: *later, Queue: *queue, Silent: true}
	if *out != "" {
		if cmd.Dir, err = filepath.Abs(*out); err != nil {
			return err
		}
	}
	// A .torrent file on this computer: the app takes its path, made
	// absolute here (the app's folder is not ours).
	if strings.EqualFold(filepath.Ext(cmd.URL), ".torrent") && !strings.Contains(cmd.URL, "://") {
		if cmd.URL, err = filepath.Abs(cmd.URL); err != nil {
			return err
		}
	}
	answer, err := askApp(cmd)
	if err != nil {
		return err
	}
	if jsonOut {
		emit(map[string]any{"added": cmd.URL, "answer": answer})
	} else {
		fmt.Fprintln(stdout, "Handed to Yellow Flash: "+cmd.URL)
	}
	return nil
}

// cmdPair: a new pairing code from the running app, printed for a person.
func cmdPair(_ context.Context, args []string) error {
	if len(args) != 0 {
		return usageErr("no arguments")
	}
	answer, err := askApp(ipc.Command{Pair: true})
	if err != nil {
		return err
	}
	p, e, err := ipc.ParseAnswer[ipc.PairAnswer](answer)
	switch {
	case err != nil:
		return err
	case e != nil:
		if jsonOut {
			fmt.Fprintln(stdout, answer)
		} else {
			fmt.Fprintln(stderr, e.Error)
		}
		return errReported
	case jsonOut:
		fmt.Fprintln(stdout, answer)
		return nil
	}
	fmt.Fprint(stdout, p.QR)
	fmt.Fprintf(stdout, "Open %s on the phone (or scan the code above),\n", strings.Join(p.URLs, "  or  "))
	fmt.Fprintf(stdout, "then enter the code %s %s (it works once, for %d minutes).\n", p.Code[:4], p.Code[4:], p.Minutes)
	return nil
}
