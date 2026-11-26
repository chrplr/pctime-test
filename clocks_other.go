// Copyright (2026) Christophe Pallier <christophe@pallier.org>
// Licensed under the Apache License, Version 2.0 (see LICENSE.txt).

//go:build !linux && !darwin

package main

import "time"

// havePOSIXClocks: there are no CLOCK_MONOTONIC_RAW and CLOCK_MONOTONIC here
// (Windows); their columns stay blank.
const havePOSIXClocks = false

func readRaw() uint64      { return 0 }
func readMono() uint64     { return 0 }
func readRealtime() uint64 { return uint64(time.Now().UnixNano()) }
