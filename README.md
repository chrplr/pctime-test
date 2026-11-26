# pctime-test: is the stimulus PC's clock drifting?

When a stimulus PC logs its events with its own clock, and a recording device
(MEG, EEG amplifier, eye tracker) records the triggers the PC sends, the two
time bases should agree to within a few tens of ppm (parts per million). Two
crystal clocks are rarely further apart than that. A larger drift means that
one of them is wrong. Fitting the log against the triggers can show that there
*is* a drift, but not *which* clock is wrong.

`pctime-test` measures the PC's side against an outside reference, NTP time.
If the PC's clock is within about 10 ppm of true time, the PC is ruled out, and
the recording device's clock (its sampling rate) is the one to check.

It measures four clocks:

| Clock | What it is |
|---|---|
| `sdl` | `SDL_GetTicksNS`, the clock of SDL3, which timestamps the flips and key events of programs built on SDL3 (e.g. goxpyriment experiments) |
| `raw` | `CLOCK_MONOTONIC_RAW`: the hardware counter (TSC) as the kernel calibrated it at boot, never corrected by NTP |
| `mono` | `CLOCK_MONOTONIC`: the same, with NTP's frequency corrections |
| `real` | `CLOCK_REALTIME`: wall-clock time, also corrected (and possibly stepped) by NTP |

`raw` and `mono` exist on Linux and macOS only; on Windows those columns stay
blank.

## How it works

Every `-interval` (10 s), for `-duration` (1 h), the program sends one SNTP
request to `-server`. It reads the four clocks just before the request and
just after the reply. The reply gives the offset θ of the local clock from
the server's, and the round-trip delay δ. The true time at the midpoint of the
exchange is that midpoint (on `CLOCK_REALTIME`) plus θ. Each clock's value at
that moment is the mean of its two reads.

At the end, a least-squares line of (clock − NTP time) against NTP time gives
each clock's error, in ppm: µs gained per second. The fit uses only the
samples with a round-trip delay of at most 1.5 times the median, because a
long round trip is usually an asymmetric one, which biases θ.

The slopes of `sdl` against `raw` and against `mono` involve no network. The
one that is 0 identifies the clock SDL reads. If it is `CLOCK_MONOTONIC_RAW`
(SDL3's source suggests so on Linux; the measurement settles it), the
programs' timestamps carry the hardware clock's error *uncorrected*, even on a
PC whose NTP is perfectly synchronised.

Over one hour, with a few ms of network noise, the slope is measured to a
fraction of a ppm on a local network and to a few ppm over the internet. That
is ample for telling a 5 ppm clock from a 500 ppm one.

## Running it

Go 1.25 or later. The SDL3 library is bundled (go-sdl3 extracts it at run
time), so nothing else is needed, and no window is opened.

```bash
go run . -server 192.0.2.1                      # 1 hour, a sample every 10 s
go run . -server 192.0.2.1 -duration 2m         # a quick check first
go build -o pctime-test . && ./pctime-test -h   # or build it
```

(`192.0.2.1` stands for your NTP server, e.g. the local gateway.)

| Flag | Default | Meaning |
|---|---|---|
| `-server` | `pool.ntp.org` | NTP server. The name is resolved once; the same address is used for the whole run |
| `-interval` | `10s` | Time between samples |
| `-duration` | `1h` | Length of the run. Ctrl-C ends it earlier, and still writes the summary |
| `-timeout` | `2s` | Longest wait for a reply |
| `-o` | `pctime-<host>-<date>.csv` | Output file |
| `-version` | | Print the version and exit |

**Choosing the server.**
- Many institutional networks block outgoing NTP (UDP port 123) to the
  internet. When every exchange fails with "no route to host" or "network is
  unreachable", use a local server instead: often the gateway. `chronyc -n
  sources` lists the servers this PC can reach; the reachable ones have a
  non-zero `Reach`.
- The result can be no better than the server's own clock, so prefer a low
  stratum (shown in the CSV).
- Do not use a public pool with an interval much shorter than 10 s: servers
  answer abusive clients with a Kiss-o'-Death packet (e.g. `RATE`), which
  ends the run.

**Conditions.**
- The ppm of a quartz clock changes by a few ppm with temperature. Run the
  test where and how the PC is used: plugged in, warmed up, with its usual
  power settings.
- Note what NTP was doing. The CSV header records it at the start and the end
  of the run: the clocksource, `timedatectl`'s `NTPSynchronized`, and the
  output of `chronyc tracking`. `raw` is unaffected by NTP; `mono` and `real`
  are not.

## Reading the result

The summary is printed and appended to the CSV as `#` lines, for example (the
figures here are made up):

```
summary: 360 samples over 3590 s; the fits against NTP time use the 331 with delay <= 6.120 ms (1.5 x the median, 4.080 ms)
error of each clock against NTP time (+ = runs fast), ppm ± standard error, residual SD:
  sdl  (SDL_GetTicksNS, the SDL programs' clock)     +2.610 ± 0.040 ppm, resid 0.420 ms (n=331)
  raw  (CLOCK_MONOTONIC_RAW)                         +2.610 ± 0.040 ppm, resid 0.420 ms (n=331)
  mono (CLOCK_MONOTONIC)                             +0.010 ± 0.040 ppm, resid 0.420 ms (n=331)
  real (CLOCK_REALTIME)                              +0.010 ± 0.040 ppm, resid 0.420 ms (n=331)
SDL's clock against the kernel's (all samples, no network involved):
  sdl - raw:  +0.0000 ppm, resid 0.30 us
  sdl - mono: +2.6000 ppm, resid 0.30 us
  -> SDL reads CLOCK_MONOTONIC_RAW (not corrected by NTP)
verdict: SDL's clock is +2.6 ppm from NTP time, within 10 ppm: this PC's clock cannot explain a drift of hundreds of ppm.
```

- **`+` means the clock runs fast**: it counts more than one second per true
  second. The log of a PC whose clock runs fast gives intervals that are too
  long compared with the recording.
- **The verdict compares `sdl` with 10 ppm, using two standard errors.** If
  the result is "too uncertain", run longer or use a closer server.
- **If the PC's clock is within 10 ppm but the log and the recording still
  drift apart by much more,** the recording device's sampling rate is the
  suspect. Check it against an independent reference fed into the recording.
  For example: a 1 Hz pulse train from a function generator or a GPS receiver,
  or TTL pulses from a PC whose clock this tool has just measured, timed on
  `CLOCK_REALTIME` with NTP synchronised, over 20-30 min.
- **If `mono` is close to 0 ppm but `raw` (and so `sdl`) is far off,** the
  kernel's TSC calibration is wrong, and NTP is hiding it from the system
  clock. Look for `tsc:` lines in `journalctl -k`.

Until the faulty clock is found, place events in the recording from the
triggers, never from offsets in the PC's log.

## Output

One row per sample, after the `#` header lines:

| Column | Meaning |
|---|---|
| `sample` | Index |
| `utc` | The true time of the sample, UTC |
| `ntp_unix_ns` | The same, in ns since 1970 |
| `sdl_ns`, `raw_ns`, `mono_ns`, `real_ns` | Each clock at that moment (midpoint of the reads before and after the exchange), in ns |
| `offset_ns` | SNTP offset θ: the server's time minus the local `CLOCK_REALTIME` |
| `delay_ns` | Round-trip delay δ |
| `stratum`, `refid` | The server's stratum and reference ID |

## License

Apache License 2.0, see [LICENSE.txt](LICENSE.txt).
