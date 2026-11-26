// Copyright (2026) Christophe Pallier <christophe@pallier.org>
// Licensed under the Apache License, Version 2.0 (see LICENSE.txt).

package main

import (
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestNTPTimestamps(t *testing.T) {
	for _, ns := range []uint64{0, 1, 999_999_999, 1_764_176_400_123_456_789} {
		got := fromNTP(toNTP(ns))
		if d := int64(got - ns); d < -1 || d > 1 {
			t.Errorf("%d -> %d (%d ns off)", ns, got, d)
		}
	}
	// 1970-01-01 is 2208988800 s after 1900-01-01.
	if got := toNTP(0) >> 32; got != ntpEpochOffset {
		t.Errorf("Unix epoch: %d NTP seconds, want %d", got, ntpEpochOffset)
	}
}

// reply builds a server packet for a request sent at t1.
func serverReply(t1, t2, t3 uint64, stratum byte, ref string) []byte {
	p := make([]byte, packetSize)
	p[0] = version4<<3 | modeServer
	p[1] = stratum
	copy(p[12:16], ref)
	binary.BigEndian.PutUint64(p[24:], toNTP(t1))
	binary.BigEndian.PutUint64(p[32:], toNTP(t2))
	binary.BigEndian.PutUint64(p[40:], toNTP(t3))
	return p
}

func TestParseReply(t *testing.T) {
	const t1 = 1_764_176_400_000_000_000
	req := request(t1)
	if req[0] != 0x23 || len(req) != packetSize {
		t.Fatalf("request: first byte %#x, %d bytes", req[0], len(req))
	}
	r, err := parseReply(serverReply(t1, t1+5_000_000, t1+5_100_000, 2, "\x0a\x00\x00\x01"), t1)
	if err != nil {
		t.Fatal(err)
	}
	if d := int64(r.t2 - (t1 + 5_000_000)); d < -1 || d > 1 {
		t.Errorf("t2 off by %d ns", d)
	}
	if r.stratum != 2 || r.refID != "10.0.0.1" {
		t.Errorf("stratum %d refid %q", r.stratum, r.refID)
	}
	if _, err := parseReply(serverReply(t1+1000, t1, t1, 2, ""), t1); err == nil {
		t.Error("a reply to another request was accepted")
	}
	if _, err := parseReply(serverReply(t1, t1, t1, 0, "RATE"), t1); !errors.Is(err, errKoD) || !strings.Contains(err.Error(), "RATE") {
		t.Errorf("kiss-o'-death: %v", err)
	}
	if _, err := parseReply(req, t1); err == nil {
		t.Error("a client packet was accepted as a reply")
	}
}

func TestOffsetDelay(t *testing.T) {
	// The local clock is 3 ms behind; 10 ms each way, 1 ms at the server.
	const local = 1_000_000_000
	t1 := uint64(local)
	t2 := t1 + 3_000_000 + 10_000_000
	t3 := t2 + 1_000_000
	t4 := t1 + 21_000_000
	off, del := offsetDelay(t1, t2, t3, t4)
	if off != 3_000_000 || del != 20_000_000 {
		t.Errorf("offset %d delay %d, want 3000000 20000000", off, del)
	}
}

// A clock running 485 ppm fast, sampled every 10 s for an hour, with ms of
// network noise, is measured to well within 1 ppm.
func TestSummaryPPM(t *testing.T) {
	var samples []sample
	for i := range 360 {
		ntp := uint64(1_764_176_400e9) + uint64(i)*10e9
		el := float64(i) * 10e9
		noise := 1e6 * math.Sin(float64(i)*1.7) // ±1 ms
		s := sample{
			ntp:   ntp + uint64(int64(noise)),
			delay: 10_000_000,
			c: clocks{
				sdl:  uint64(5e9 + el*(1+485e-6)),
				raw:  uint64(7e9 + el*(1+485e-6)),
				mono: uint64(9e9 + el*(1+480e-6)),
				real: ntp,
			},
		}
		samples = append(samples, s)
	}
	k, _ := kept(samples)
	f := against(k, sdlOf, ntpOf)
	if math.Abs(f.slope-485) > 0.5 || f.n != 360 {
		t.Errorf("SDL slope %.3f ppm (n=%d), want 485", f.slope, f.n)
	}
	lines := strings.Join(summary(samples, true), "\n")
	for _, want := range []string{"SDL reads CLOCK_MONOTONIC_RAW", "off by more than 10 ppm"} {
		if !strings.Contains(lines, want) {
			t.Errorf("summary lacks %q:\n%s", want, lines)
		}
	}
}

func TestWhichClock(t *testing.T) {
	f := func(s float64) fit { return fit{slope: s} }
	for _, c := range []struct {
		raw, mono float64
		want      string
	}{
		{0, -2.6, "CLOCK_MONOTONIC_RAW"},
		{2.6, 0, "CLOCK_MONOTONIC (corrected"},
		{0.01, 0.02, "cannot tell"},
		{3, 5, "neither"},
	} {
		if got := whichClock(f(c.raw), f(c.mono)); !strings.Contains(got, c.want) {
			t.Errorf("raw %v mono %v: %q, want %q", c.raw, c.mono, got, c.want)
		}
	}
}

func TestKeptDelay(t *testing.T) {
	var samples []sample
	for _, d := range []int64{10, 11, 12, 10, 50} {
		samples = append(samples, sample{delay: d * 1e6})
	}
	k, cut := kept(samples)
	if len(k) != 4 || cut != 1.5*11e6 {
		t.Errorf("kept %d with cut %v, want 4 with 16.5 ms", len(k), cut)
	}
}
