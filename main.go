// Copyright (2026) Christophe Pallier <christophe@pallier.org>
// Licensed under the Apache License, Version 2.0 (see LICENSE.txt).

// Command pctime-test measures the clocks of this PC against NTP time: SDL's
// clock (SDL_GetTicksNS, which timestamps the flips and key events of the MEG
// programs, and so their onset_ms), CLOCK_MONOTONIC_RAW, CLOCK_MONOTONIC and
// CLOCK_REALTIME. Every -interval, for -duration, it sends one SNTP request to
// -server and reads the four clocks just before sending and just after the
// reply. The slope of (clock - NTP time) over the run is the clock's error in
// ppm. The slopes of SDL's clock against the kernel's show which of them SDL
// reads.
//
// Its purpose is to find which side of a timing drift is at fault: when the
// timestamps written by a stimulus PC and the triggers recorded by another
// device (an MEG, an EEG amplifier) drift apart, it measures the PC's side
// against an outside reference. See README.md.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Zyko0/go-sdl3/bin/binsdl"
	"github.com/Zyko0/go-sdl3/sdl"
)

// clocks are the four clocks read together, in ns.
type clocks struct {
	sdl, raw, mono, real uint64
}

// readBefore and readAfter read the four clocks in mirror order, so that
// their midpoints refer to the same moment.
func readBefore() clocks {
	var c clocks
	c.sdl = sdl.TicksNS()
	c.raw = readRaw()
	c.mono = readMono()
	c.real = readRealtime()
	return c
}

func readAfter() clocks {
	var c clocks
	c.real = readRealtime()
	c.mono = readMono()
	c.raw = readRaw()
	c.sdl = sdl.TicksNS()
	return c
}

func mid(a, b uint64) uint64 { return a/2 + b/2 + (a%2+b%2)/2 }

// sample is one SNTP exchange: the true time at its midpoint, the clocks at
// the same moment, and the exchange's offset and delay.
type sample struct {
	ntp           uint64 // Unix ns: the local REALTIME midpoint plus the offset
	c             clocks // midpoints of the reads before and after
	offset, delay int64  // ns
	stratum       int
	refID         string
}

// exchange runs one SNTP request on conn.
func exchange(conn net.Conn, timeout time.Duration) (sample, error) {
	buf := make([]byte, 512)
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return sample{}, err
	}
	before := readBefore()
	if _, err := conn.Write(request(before.real)); err != nil {
		return sample{}, err
	}
	n, err := conn.Read(buf)
	after := readAfter()
	if err != nil {
		return sample{}, err
	}
	r, err := parseReply(buf[:n], before.real)
	if err != nil {
		return sample{}, err
	}
	offset, delay := offsetDelay(before.real, r.t2, r.t3, after.real)
	s := sample{
		c: clocks{
			sdl:  mid(before.sdl, after.sdl),
			raw:  mid(before.raw, after.raw),
			mono: mid(before.mono, after.mono),
			real: mid(before.real, after.real),
		},
		offset: offset, delay: delay, stratum: r.stratum, refID: r.refID,
	}
	s.ntp = uint64(int64(s.c.real) + offset)
	return s, nil
}

func main() {
	server := flag.String("server", "pool.ntp.org", "NTP server; the name is resolved once and the same address is used for the whole run")
	interval := flag.Duration("interval", 10*time.Second, "Time between two samples")
	duration := flag.Duration("duration", time.Hour, "Length of the run (Ctrl-C ends it earlier, with the summary)")
	timeout := flag.Duration("timeout", 2*time.Second, "Longest wait for a reply")
	out := flag.String("o", "", "CSV file to write (default pctime-<host>-<date>.csv in the current directory)")
	showVersion := flag.Bool("version", false, "Print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(versionString())
		return
	}
	if *interval <= 0 || *duration <= 0 || *timeout <= 0 {
		log.Fatal("-interval, -duration and -timeout must be positive")
	}

	// The SDL of the MEG programs: the copy bundled with go-sdl3, as
	// goxpyriment loads it. SDL_GetTicksNS needs no SDL_Init.
	lib := binsdl.Load()
	defer lib.Unload()

	addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(*server, "123"))
	if err != nil {
		log.Fatalf("-server %s: %v", *server, err)
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		log.Fatalf("-server %s: %v", *server, err)
	}
	defer conn.Close()

	host, _ := os.Hostname()
	start := time.Now()
	path := *out
	if path == "" {
		path = fmt.Sprintf("pctime-%s-%s.csv", host, start.Format("20060102-150405"))
	}
	f, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()

	cond := []string{
		"pctime-test: the clocks of this PC against NTP time",
		"date: " + start.UTC().Format(time.RFC3339),
		"host: " + host,
		"os: " + runtime.GOOS + "/" + runtime.GOARCH,
		"program: " + versionString(),
		"sdl: " + sdl.GetVersion().String(),
		fmt.Sprintf("server: %s (%s), interval %v, duration %v, timeout %v", *server, addr.IP, *interval, *duration, *timeout),
	}
	cond = append(cond, systemState()...)
	for _, line := range cond {
		fmt.Fprintln(w, "# "+line)
		log.Print(line)
	}
	fmt.Fprintln(w, "sample,utc,ntp_unix_ns,sdl_ns,raw_ns,mono_ns,real_ns,offset_ns,delay_ns,stratum,refid")
	w.Flush()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	deadline := time.After(*duration)
	tick := time.NewTicker(*interval)
	defer tick.Stop()

	var samples []sample
	failures := 0
	ended := ""
loop:
	for i := 0; ; i++ {
		s, err := exchange(conn, *timeout)
		switch {
		case errors.Is(err, errKoD):
			ended = "the server sent a Kiss-o'-Death: " + err.Error()
			log.Print(ended)
			break loop
		case err != nil:
			failures++
			log.Printf("sample %d: %v", i, err)
		default:
			samples = append(samples, s)
			writeRow(w, len(samples)-1, s)
			w.Flush()
			log.Print(progress(samples))
		}
		select {
		case <-ctx.Done():
			ended = "stopped by a signal"
			break loop
		case <-deadline:
			ended = "end of -duration"
			break loop
		case <-tick.C:
		}
	}

	lines := []string{fmt.Sprintf("end: %s, after %v; %d samples, %d failed exchanges",
		ended, time.Since(start).Round(time.Second), len(samples), failures)}
	lines = append(lines, summary(samples, havePOSIXClocks)...)
	lines = append(lines, systemState()...)
	for _, line := range lines {
		fmt.Fprintln(w, "# "+line)
		fmt.Println(line)
	}
	fmt.Println("data: " + path)
}

func writeRow(w io.Writer, i int, s sample) {
	raw, mono := "", ""
	if havePOSIXClocks {
		raw, mono = strconv.FormatUint(s.c.raw, 10), strconv.FormatUint(s.c.mono, 10)
	}
	fmt.Fprintf(w, "%d,%s,%d,%d,%s,%s,%d,%d,%d,%d,%s\n", i,
		time.Unix(0, int64(s.ntp)).UTC().Format("2006-01-02T15:04:05.000000Z"),
		s.ntp, s.c.sdl, raw, mono, s.c.real, s.offset, s.delay, s.stratum, s.refID)
}

// systemState is the state of the clocks' discipline, as the system reports
// it: the clocksource, whether NTP is synchronised, and chrony's tracking.
// What is not available is left out.
func systemState() []string {
	var lines []string
	if b, err := os.ReadFile("/sys/devices/system/clocksource/clocksource0/current_clocksource"); err == nil {
		lines = append(lines, "clocksource: "+strings.TrimSpace(string(b)))
	}
	if out, ok := run("timedatectl", "show", "-p", "NTPSynchronized", "--value"); ok {
		lines = append(lines, "NTPSynchronized: "+strings.TrimSpace(out))
	}
	if out, ok := run("chronyc", "-n", "tracking"); ok {
		for l := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
			lines = append(lines, "chronyc tracking: "+l)
		}
	}
	return lines
}

// run runs a command for a few seconds at most; ok is false if it failed.
func run(name string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err == nil
}

// delayCut is the longest delay kept for the fits against NTP time, as a
// multiple of the median delay: a long round trip is usually an asymmetric
// one, which biases the offset.
const delayCut = 1.5

// kept are the samples whose delay is at most delayCut times the median, and
// that cut in ns.
func kept(samples []sample) ([]sample, float64) {
	d := make([]float64, len(samples))
	for i, s := range samples {
		d[i] = float64(s.delay)
	}
	cut := delayCut * median(d)
	var k []sample
	for _, s := range samples {
		if float64(s.delay) <= cut {
			k = append(k, s)
		}
	}
	return k, cut
}

// against fits (a - b) over time, both in ns, against the NTP time of the
// samples: the slope is in ppm (µs per s) and the residual SD in µs.
func against(samples []sample, a, b func(sample) uint64) fit {
	x := make([]float64, len(samples))
	y := make([]float64, len(samples))
	for i, s := range samples {
		x[i] = float64(int64(s.ntp-samples[0].ntp)) / 1e9
		y[i] = (float64(int64(a(s)-a(samples[0]))) - float64(int64(b(s)-b(samples[0])))) / 1e3
	}
	return lineFit(x, y)
}

var (
	ntpOf  = func(s sample) uint64 { return s.ntp }
	sdlOf  = func(s sample) uint64 { return s.c.sdl }
	rawOf  = func(s sample) uint64 { return s.c.raw }
	monoOf = func(s sample) uint64 { return s.c.mono }
	realOf = func(s sample) uint64 { return s.c.real }
)

// progress is the running estimate printed after each sample.
func progress(samples []sample) string {
	s := samples[len(samples)-1]
	line := fmt.Sprintf("sample %d: offset %+.3f ms, delay %.3f ms", len(samples)-1,
		float64(s.offset)/1e6, float64(s.delay)/1e6)
	k, _ := kept(samples)
	if f := against(k, sdlOf, ntpOf); f.n >= 3 {
		line += fmt.Sprintf("; SDL clock so far %+.2f ± %.2f ppm (n=%d, %.0f s)", f.slope, f.se, f.n, f.span)
	}
	return line
}

// okPPM is the error below which the PC's clock cannot explain the drift.
const okPPM = 10.0

// summary is the result of the run, as lines.
func summary(samples []sample, posix bool) []string {
	if len(samples) < 3 {
		return []string{"summary: fewer than 3 samples, no fit"}
	}
	k, cut := kept(samples)
	d := make([]float64, len(samples))
	for i, s := range samples {
		d[i] = float64(s.delay)
	}
	lines := []string{
		fmt.Sprintf("summary: %d samples over %.0f s; the fits against NTP time use the %d with delay <= %.3f ms (%.1f x the median, %.3f ms)",
			len(samples), float64(samples[len(samples)-1].ntp-samples[0].ntp)/1e9, len(k), cut/1e6, delayCut, median(d)/1e6),
		"error of each clock against NTP time (+ = runs fast), ppm ± standard error, residual SD:",
	}
	type named struct {
		name string
		of   func(sample) uint64
	}
	all := []named{{"sdl  (SDL_GetTicksNS, the SDL programs' clock)", sdlOf}}
	if posix {
		all = append(all, named{"raw  (CLOCK_MONOTONIC_RAW)", rawOf}, named{"mono (CLOCK_MONOTONIC)", monoOf})
	}
	all = append(all, named{"real (CLOCK_REALTIME)", realOf})
	var sdlFit fit
	for i, c := range all {
		f := against(k, c.of, ntpOf)
		if i == 0 {
			sdlFit = f
		}
		lines = append(lines, fmt.Sprintf("  %-48s %+8.3f ± %.3f ppm, resid %.3f ms (n=%d)", c.name, f.slope, f.se, f.residSD/1e3, f.n))
	}

	if posix {
		// No network here: every sample counts.
		dr, dm := against(samples, sdlOf, rawOf), against(samples, sdlOf, monoOf)
		lines = append(lines, "SDL's clock against the kernel's (all samples, no network involved):",
			fmt.Sprintf("  sdl - raw:  %+.4f ppm, resid %.2f us", dr.slope, dr.residSD),
			fmt.Sprintf("  sdl - mono: %+.4f ppm, resid %.2f us", dm.slope, dm.residSD))
		lines = append(lines, "  -> "+whichClock(dr, dm))
	}

	switch {
	case math.IsNaN(sdlFit.slope):
		lines = append(lines, "verdict: no fit")
	case math.Abs(sdlFit.slope)+2*sdlFit.se <= okPPM:
		lines = append(lines, fmt.Sprintf("verdict: SDL's clock is %+.1f ppm from NTP time, within %.0f ppm: this PC's clock cannot explain a drift of hundreds of ppm.", sdlFit.slope, okPPM))
	case math.Abs(sdlFit.slope)-2*sdlFit.se > okPPM:
		lines = append(lines, fmt.Sprintf("verdict: SDL's clock is %+.1f ppm from NTP time: this PC's clock is off by more than %.0f ppm.", sdlFit.slope, okPPM))
	default:
		lines = append(lines, fmt.Sprintf("verdict: SDL's clock is %+.1f ± %.1f ppm from NTP time: too uncertain to decide; run longer, or use a closer server.", sdlFit.slope, sdlFit.se))
	}
	return lines
}

// sameRate is the difference of slope below which RAW and MONOTONIC are
// taken to run at the same rate.
const sameRate = 0.05 // ppm

// whichClock says which kernel clock SDL's ticks follow, from their slopes
// against RAW and MONOTONIC.
func whichClock(raw, mono fit) string {
	switch {
	case math.IsNaN(raw.slope) || math.IsNaN(mono.slope):
		return "too few samples to tell which clock SDL reads"
	case math.Abs(raw.slope-mono.slope) < sameRate:
		return "RAW and MONOTONIC ran at the same rate (no NTP frequency correction): cannot tell which one SDL reads"
	case math.Abs(raw.slope) < sameRate:
		return "SDL reads CLOCK_MONOTONIC_RAW (not corrected by NTP)"
	case math.Abs(mono.slope) < sameRate:
		return "SDL reads CLOCK_MONOTONIC (corrected by NTP)"
	}
	return "SDL follows neither CLOCK_MONOTONIC_RAW nor CLOCK_MONOTONIC"
}
