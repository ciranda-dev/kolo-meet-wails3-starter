package main

import (
	"log"
	"os"
	"runtime/debug"
)

// defaultMemoryLimit is the soft memory limit the app sets when GOMEMLIMIT is not: 2 GiB, four times the least a call
// needs (512 MiB). It makes the garbage collector work harder as the heap nears it; it does not stop an allocation.
const defaultMemoryLimit = 2 << 30

// setMemoryLimit sets the soft limit unless the environment gives one, and returns the limit in force in bytes, and
// whether the environment gave it.
func setMemoryLimit(getenv func(string) string, set func(int64) int64) (limit int64, fromEnv bool) {
	if getenv("GOMEMLIMIT") != "" {
		return set(-1), true // the runtime already applied it
	}
	set(defaultMemoryLimit)
	return defaultMemoryLimit, false
}

// applyMemoryLimit is setMemoryLimit for the process, with one log line when the app chose the limit.
func applyMemoryLimit() {
	if limit, fromEnv := setMemoryLimit(os.Getenv, debug.SetMemoryLimit); !fromEnv {
		log.Printf("soft memory limit %d MiB; set GOMEMLIMIT to change it", limit>>20)
	}
}
