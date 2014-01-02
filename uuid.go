/*
 * (c) 2014, Caoimhe Chaos <caoimhechaos@protonmail.com>,
 *	     Starship Factory. All rights reserved.
 *
 * Redistribution and use in source  and binary forms, with or without
 * modification, are permitted  provided that the following conditions
 * are met:
 *
 * * Redistributions of  source code  must retain the  above copyright
 *   notice, this list of conditions and the following disclaimer.
 * * Redistributions in binary form must reproduce the above copyright
 *   notice, this  list of conditions and the  following disclaimer in
 *   the  documentation  and/or  other  materials  provided  with  the
 *   distribution.
 * * Neither  the name  of the Starship Factory  nor the  name  of its
 *   contributors may  be used to endorse or  promote products derived
 *   from this software without specific prior written permission.
 *
 * THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
 * "AS IS"  AND ANY EXPRESS  OR IMPLIED WARRANTIES  OF MERCHANTABILITY
 * AND FITNESS  FOR A PARTICULAR  PURPOSE ARE DISCLAIMED. IN  NO EVENT
 * SHALL THE COPYRIGHT OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT,
 * INDIRECT, INCIDENTAL, SPECIAL,  EXEMPLARY, OR CONSEQUENTIAL DAMAGES
 * (INCLUDING, BUT NOT LIMITED  TO, PROCUREMENT OF SUBSTITUTE GOODS OR
 * SERVICES; LOSS OF USE,  DATA, OR PROFITS; OR BUSINESS INTERRUPTION)
 * HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT,
 * STRICT  LIABILITY,  OR  TORT  (INCLUDING NEGLIGENCE  OR  OTHERWISE)
 * ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED
 * OF THE POSSIBILITY OF SUCH DAMAGE.
 */

package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

type UUID []byte

const NUM_100NS_INTERVALS_SINCE_UUID_EPOCH = 0x01b21dd213814000

// Generate a time based UUID tailored for use in Apache Cassandra.
// The UUID will conform to the Apache Cassandra time based UUID rules
// representing the given time specified in "when". The only errors
// which could happen would be memory writing errors, which would be
// weird.
func GenTimeUUID(when *time.Time) (UUID, error) {
	var uuid *bytes.Buffer = new(bytes.Buffer)
	var stamp int64 = when.UnixNano()/100 + NUM_100NS_INTERVALS_SINCE_UUID_EPOCH
	var stampLow int64 = stamp & 0xffffffff
	var stampMid int64 = stamp & 0xffff00000000
	var stampHi int64 = stamp & 0xfff000000000000
	var err error

	var upper int64 = (stampLow << 32) | (stampMid >> 16) | (1 << 12) |
		(stampHi >> 48)

	err = binary.Write(uuid, binary.LittleEndian, upper)
	if err != nil {
		return UUID([]byte{}), err
	}
	uuid.WriteByte(0xC0)
	uuid.WriteByte(0x00)

	_, err = io.CopyN(uuid, rand.Reader, 6)
	if err != nil {
		return UUID([]byte{}), err
	}

	return UUID(uuid.Bytes()), nil
}

// Converts a bunch of bytes to an UUID. Doesn't really do a lot.
func UUIDFromBytes(b []byte) UUID {
	return UUID(b)
}

// Formats the UUID given as bytes as a string. It will be displayed as
// a group of 8, then 4, then 4 and then 8 hexadecimal digits separated
// by dashes.
func (uuid UUID) String() string {
	var ret string
	var i int

	for i = 0; i < 4; i++ {
		ret += fmt.Sprintf("%02X", uuid[i])
	}

	ret += "-"

	for i = 4; i < 6; i++ {
		ret += fmt.Sprintf("%02X", uuid[i])
	}

	ret += "-"

	for i = 6; i < 8; i++ {
		ret += fmt.Sprintf("%02X", uuid[i])
	}

	ret += "-"

	for i = 8; i < 12; i++ {
		ret += fmt.Sprintf("%02X", uuid[i])
	}

	ret += "-"

	for i = 12; i < len(uuid); i++ {
		ret += fmt.Sprintf("%02X", uuid[i])
	}

	return ret
}

// Converts the string "s" to an UUID by splitting the groups separated by
// "-" and parsing the groups which should be formatted as hexadecimal
// numbers. Essentailly, it converts dash-separated hexadecimal strings to
// bytes.
func ParseUUID(s string) (UUID, error) {
	var parts []string = strings.Split(s, "-")
	var uuid UUID = UUID(make([]byte, (len(s)-len(parts)+1)/2))
	var pos int = 0
	var i int
	var part string
	var ipart uint64
	var err error

	for _, part = range parts {
		if len(part)&1 != 0 {
			return uuid, errors.New("Part length is not a multiple of 2")
		}
		for i = 0; i < len(part); i += 2 {
			ipart, err = strconv.ParseUint(part[i:i+2], 16, 64)
			if err != nil {
				return uuid, err
			}
			uuid[pos] = byte(ipart & 0xFF)
			pos = pos + 1
		}
	}

	return uuid, nil
}
