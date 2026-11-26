// Copyright (2026) Christophe Pallier <christophe@pallier.org>
// Licensed under the Apache License, Version 2.0 (see LICENSE.txt).

//go:build linux || darwin

package main

import "golang.org/x/sys/unix"

// havePOSIXClocks: CLOCK_MONOTONIC_RAW and CLOCK_MONOTONIC can be read.
const havePOSIXClocks = true

// readClock returns a POSIX clock in ns, 0 if it cannot be read.
func readClock(id int32) uint64 {
	var ts unix.Timespec
	if err := unix.ClockGettime(id, &ts); err != nil {
		return 0
	}
	return uint64(ts.Sec)*1e9 + uint64(ts.Nsec)
}

func readRaw() uint64      { return readClock(unix.CLOCK_MONOTONIC_RAW) }
func readMono() uint64     { return readClock(unix.CLOCK_MONOTONIC) }
func readRealtime() uint64 { return readClock(unix.CLOCK_REALTIME) }
