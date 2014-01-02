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
	"strconv"
	"testing"
	"time"
)

func TestUUID(t *testing.T) {
	var uuid UUID
	var uuid2 UUID
	var uuidstr string
	var uuid2str string
	var now time.Time = time.Now()
	var i int
	var err error

	t.Parallel()

	uuid, err = GenTimeUUID(&now)
	if err != nil {
		t.Fatal(err)
	}

	uuidstr = uuid.String()

	uuid2, err = ParseUUID(uuidstr)
	if err != nil {
		t.Fatal(err)
	}

	if len(uuid) != len(uuid2) {
		t.Error("Error serializing and deserializing UUID: ", len(uuid),
			" vs. ", len(uuid2))
	}

	for i = 0; i < len(uuid); i++ {
		if uuid[i] != uuid2[i] {
			t.Error("Byte ", i, " differs: ",
				strconv.FormatUint(uint64(uuid[i]), 16), " vs. ",
				strconv.FormatUint(uint64(uuid2[i]), 16))
		}
	}

	uuid2str = uuid2.String()
	if uuidstr != uuid2str {
		t.Error("UUID strings mismatch: ", uuidstr, " vs. ", uuid2str)
	}
}
