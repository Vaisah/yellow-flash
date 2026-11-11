// Command crashy crashes on purpose, as the crash tests need: it installs
// the crash output into the data folder given, logs a line, and panics in a
// goroutine of its own (or, with "fatal", dies of a fatal runtime error).
package main

import (
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/vaisah/internal/crash"
)

func main() {
	data := os.Args[1]
	if _, err := crash.Install(data); err != nil {
		panic(err)
	}
	var logger *slog.Logger
	if logger, _ = crash.NewLogger(data); logger != nil {
		logger.Info("crashy is about to fail", "home", os.Getenv("HOME")+os.Getenv("USERPROFILE"))
	}
	if len(os.Args) > 2 && os.Args[2] == "fatal" {
		var mu sync.Mutex
		mu.Unlock() // fatal error: sync: unlock of unlocked mutex
	}
	go downloadWorker()
	time.Sleep(10 * time.Second)
}

func downloadWorker() {
	var parts []int
	_ = parts[3] // index out of range
}
