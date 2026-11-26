// Copyright (2026) Christophe Pallier <christophe@pallier.org>
// Licensed under the Apache License, Version 2.0 (see LICENSE.txt).

package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// SNTP (RFC 4330): a 48-byte packet; the client sends mode 3, the server
// answers mode 4 with its receive and transmit timestamps.
const (
	packetSize = 48
	// ntpEpochOffset is the number of seconds from 1900-01-01 (the NTP
	// epoch) to 1970-01-01 (the Unix epoch).
	ntpEpochOffset = 2208988800
	modeClient     = 3
	modeServer     = 4
	version4       = 4
)

// errKoD is a Kiss-o'-Death reply: the server asks the client to stop
// (RATE, DENY, RSTR...).
var errKoD = errors.New("kiss-o'-death")

// toNTP converts Unix ns to an NTP timestamp (32.32 fixed point, seconds since
// 1900).
func toNTP(unixNS uint64) uint64 {
	sec := unixNS/1e9 + ntpEpochOffset
	frac := (unixNS % 1e9) << 32 / 1e9
	return sec<<32 | frac
}

// fromNTP converts an NTP timestamp to Unix ns. Seconds below 2^31 are taken
// as era 1 (from 2036), as RFC 4330 suggests.
func fromNTP(ts uint64) uint64 {
	sec, frac := ts>>32, ts&0xffffffff
	if sec < 1<<31 {
		sec += 1 << 32
	}
	return (sec-ntpEpochOffset)*1e9 + (frac*1e9+1<<31)>>32
}

// request is a client packet whose transmit timestamp is t1 (Unix ns); the
// server copies it into the originate field of its reply, which ties the
// reply to the request.
func request(t1 uint64) []byte {
	p := make([]byte, packetSize)
	p[0] = version4<<3 | modeClient // LI 0
	binary.BigEndian.PutUint64(p[40:], toNTP(t1))
	return p
}

// reply is what parseReply reads from the server's packet: its receive (t2)
// and transmit (t3) times in Unix ns, its stratum and reference ID.
type reply struct {
	t2, t3  uint64
	stratum int
	refID   string
}

// parseReply checks the server's packet against the request sent at t1.
func parseReply(p []byte, t1 uint64) (reply, error) {
	if len(p) < packetSize {
		return reply{}, fmt.Errorf("short reply: %d bytes", len(p))
	}
	if mode := p[0] & 7; mode != modeServer {
		return reply{}, fmt.Errorf("reply mode %d, want %d", mode, modeServer)
	}
	r := reply{stratum: int(p[1])}
	if r.stratum == 0 {
		r.refID = strings.TrimRight(string(p[12:16]), "\x00")
		return r, fmt.Errorf("%w (%s)", errKoD, r.refID)
	}
	if orig := binary.BigEndian.Uint64(p[24:]); orig != toNTP(t1) {
		return reply{}, errors.New("reply does not answer the request (originate timestamp)")
	}
	if p[0]>>6 == 3 {
		return reply{}, errors.New("server clock not synchronised (leap indicator 3)")
	}
	r.t2 = fromNTP(binary.BigEndian.Uint64(p[32:]))
	r.t3 = fromNTP(binary.BigEndian.Uint64(p[40:]))
	if r.stratum == 1 {
		r.refID = strings.TrimRight(string(p[12:16]), "\x00")
	} else {
		r.refID = fmt.Sprintf("%d.%d.%d.%d", p[12], p[13], p[14], p[15])
	}
	return r, nil
}

// offsetDelay are the SNTP offset of the local clock (true time minus local
// time) and the round-trip delay, from the client's send and receive times
// (t1, t4) and the server's (t2, t3), all in ns.
func offsetDelay(t1, t2, t3, t4 uint64) (offset, delay int64) {
	a, b, c, d := int64(t1), int64(t2), int64(t3), int64(t4)
	return ((b - a) + (c - d)) / 2, (d - a) - (c - b)
}
