/*
 * (c) 2013, Caoimhe Chaos <caoimhechaos@protonmail.com>,
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
	"database/cassandra"
	"encoding/binary"
	"expvar"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"ancient-solutions.com/ancientauth"
	"code.google.com/p/goprotobuf/proto"
)

const NUM_100NS_INTERVALS_SINCE_UUID_EPOCH = 0x01b21dd213814000

// Number of errors which occurred adding products, mapped by type.
var productAddErrors *expvar.Map = expvar.NewMap("num-product-add-errors")

type ProductAddAPI struct {
	authenticator *ancientauth.Authenticator
	client        *cassandra.RetryCassandraClient
	scope         string
}

func GenTimeUUID(when *time.Time) ([]byte, error) {
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
		return []byte{}, err
	}
	uuid.WriteByte(0xC0)
	uuid.WriteByte(0x00)

	_, err = io.CopyN(uuid, rand.Reader, 6)
	if err != nil {
		return []byte{}, err
	}

	return uuid.Bytes(), nil
}

func (self *ProductAddAPI) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var uuid []byte
	var mmap map[string]map[string][]*cassandra.Mutation
	var mutations []*cassandra.Mutation
	var mutation *cassandra.Mutation
	var col *cassandra.Column
	var ire *cassandra.InvalidRequestException
	var ue *cassandra.UnavailableException
	var te *cassandra.TimedOutException
	var prodname, barcode string
	var now time.Time = time.Now()
	var err error
	var match bool

	numRequests.Add(1)
	numAPIRequests.Add(1)

	// Check the user is in the reqeuested scope.
	if !self.authenticator.IsAuthenticatedScope(req, self.scope) {
		numDisallowedScope.Add(1)
		http.Error(w,
			"You are not in the right group to access this resource",
			http.StatusForbidden)
		return
	}

	// Check if the product name has been specified.
	prodname = req.PostFormValue("prodname")
	if len(prodname) <= 0 {
		http.Error(w, "Product name empty", http.StatusNotAcceptable)
		return
	}

	// Check if the barcode has been given. If it was, it needs to be
	// numeric (EAN-13). If we find different types of barcodes we can
	// always revise this.
	barcode = strings.Replace(req.PostFormValue("barcode"), " ", "", -1)
	if len(barcode) > 0 {
		match, err = regexp.MatchString("^[0-9]+$", barcode)
		if err != nil {
			productAddErrors.Add(err.Error(), 1)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !match {
			productAddErrors.Add("barcode-format-error", 1)
			http.Error(w, "Barcode should only contain numbers",
				http.StatusNotAcceptable)
			return
		}
	}

	col = cassandra.NewColumn()
	col.Name = []byte("name")
	col.Value = []byte(prodname)
	col.Timestamp = now.Unix()
	mutation = cassandra.NewMutation()
	mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
	mutation.ColumnOrSupercolumn.Column = col
	mutations = append(mutations, mutation)

	if len(barcode) > 0 {
		var codes *Barcodes = new(Barcodes)
		codes.Barcode = append(codes.Barcode, barcode)

		col = cassandra.NewColumn()
		col.Name = []byte("barcodes")
		col.Value, err = proto.Marshal(codes)
		if err != nil {
			productAddErrors.Add(err.Error(), 1)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		col.Timestamp = now.Unix()
		mutation = cassandra.NewMutation()
		mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
		mutation.ColumnOrSupercolumn.Column = col
		mutations = append(mutations, mutation)
	}

	uuid, err = GenTimeUUID(&now)
	if err != nil {
		productAddErrors.Add(err.Error(), 1)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	mmap = make(map[string]map[string][]*cassandra.Mutation)
	mmap[string(uuid)] = make(map[string][]*cassandra.Mutation)
	mmap[string(uuid)]["products"] = mutations

	mutations = make([]*cassandra.Mutation, 0)
	col = cassandra.NewColumn()
	col.Name = []byte("product")
	col.Value = uuid
	col.Timestamp = now.Unix()
	mutation = cassandra.NewMutation()
	mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
	mutation.ColumnOrSupercolumn.Column = col
	mutations = append(mutations, mutation)
	mmap[prodname] = make(map[string][]*cassandra.Mutation)
	mmap[prodname]["products_byname"] = mutations

	if len(barcode) > 0 {
		mmap[barcode] = make(map[string][]*cassandra.Mutation)
		mmap[barcode]["products_bybarcode"] = mutations
	}

	ire, ue, te, err = self.client.BatchMutate(mmap,
		cassandra.ConsistencyLevel_QUORUM)
	if ire != nil {
		log.Println("Invalid request: ", ire.Why)
		productAddErrors.Add(ire.Why, 1)
		return
	}
	if ue != nil {
		log.Println("Unavailable")
		productAddErrors.Add("unavailable", 1)
		return
	}
	if te != nil {
		log.Println("Request to database backend timed out")
		productAddErrors.Add("timeout", 1)
		return
	}
	if err != nil {
		log.Println("Generic error: ", err)
		productAddErrors.Add(err.Error(), 1)
		return
	}
}
