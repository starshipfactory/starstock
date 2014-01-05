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
	"database/cassandra"
	"encoding/binary"
	"encoding/json"
	"expvar"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ancient-solutions.com/ancientauth"
	"code.google.com/p/goprotobuf/proto"
)

// Number of errors which occurred viewing products, mapped by type.
var productViewErrors *expvar.Map = expvar.NewMap("num-product-view-errors")

// Number of errors which occurred editing products, mapped by type.
var productEditErrors *expvar.Map = expvar.NewMap("num-product-edit-errors")

type Product struct {
	Name     string
	Price    float64
	Barcodes []string
	VendorId string
	Stock    uint64
}

type ProductViewAPI struct {
	authenticator *ancientauth.Authenticator
	client        *cassandra.RetryCassandraClient
	scope         string
}

type ProductEditAPI struct {
	authenticator *ancientauth.Authenticator
	client        *cassandra.RetryCassandraClient
	scope         string
}

func (self *ProductViewAPI) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var cp *cassandra.ColumnParent
	var pred *cassandra.SlicePredicate
	var res []*cassandra.ColumnOrSuperColumn
	var csc *cassandra.ColumnOrSuperColumn
	var ire *cassandra.InvalidRequestException
	var ue *cassandra.UnavailableException
	var te *cassandra.TimedOutException
	var prod Product
	var err error
	var uuidstr string = req.FormValue("id")
	var ts int64 = 0
	var uuid UUID

	var rawdata []byte

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

	if len(uuidstr) <= 0 {
		http.Error(w, "Requested UUID empty", http.StatusNotAcceptable)
		return
	}

	uuid, err = ParseUUID(uuidstr)
	if err != nil {
		http.Error(w, "Requested UUID invalid", http.StatusNotAcceptable)
		return
	}

	cp = cassandra.NewColumnParent()
	cp.ColumnFamily = "products"

	pred = cassandra.NewSlicePredicate()
	pred.ColumnNames = [][]byte{
		[]byte("name"), []byte("price"), []byte("vendor"),
		[]byte("barcodes"), []byte("stock"),
	}

	res, ire, ue, te, err = self.client.GetSlice([]byte(uuid), cp, pred,
		cassandra.ConsistencyLevel_ONE)
	if ire != nil {
		log.Print("Invalid request: ", ire.Why)
		productViewErrors.Add(ire.Why, 1)
		return
	}
	if ue != nil {
		log.Print("Unavailable")
		productViewErrors.Add("unavailable", 1)
		return
	}
	if te != nil {
		log.Print("Request to database backend timed out")
		productViewErrors.Add("timeout", 1)
		return
	}
	if err != nil {
		log.Print("Generic error: ", err)
		productViewErrors.Add(err.Error(), 1)
		return
	}

	for _, csc = range res {
		var col = csc.Column
		var cname string
		if !csc.IsSetColumn() {
			continue
		}

		cname = string(col.Name)
		if col.IsSetTimestamp() && col.Timestamp > ts {
			ts = col.Timestamp
		}

		if cname == "name" {
			prod.Name = string(col.Value)
		} else if cname == "price" {
			var buf *bytes.Buffer = bytes.NewBuffer(col.Value)
			err = binary.Read(buf, binary.BigEndian, &prod.Price)
			if err != nil {
				log.Print("Row ", uuid.String(), " price is invalid")
				productViewErrors.Add("corrupted-price", 1)
			}
		} else if cname == "vendor" {
			prod.VendorId = UUID(col.Value).String()
		} else if cname == "barcodes" {
			var bc Barcodes
			err = proto.Unmarshal(col.Value, &bc)
			if err != nil {
				log.Print("Row ", uuid.String(), " barcode is invalid")
				productViewErrors.Add("corrupted-barcode", 1)
				return
			}

			for _, code := range bc.Barcode {
				prod.Barcodes = append(prod.Barcodes, code)
			}
		} else if cname == "stock" {
			var buf *bytes.Buffer = bytes.NewBuffer(col.Value)
			err = binary.Read(buf, binary.BigEndian, &prod.Stock)
			if err != nil {
				log.Print("Row ", uuid.String(), " stock is invalid")
				productViewErrors.Add("corrupted-stock", 1)
			}
		}
	}

	rawdata, err = json.Marshal(prod)
	if err != nil {
		log.Print("Error marshalling JSON: ", err)
		numJSONMarshalErrors.Add(1)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Add("Content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(rawdata)
	if err != nil {
		log.Print("Error writing JSON response: ", err)
		numHTTPWriteErrors.Add(err.Error(), 1)
	}
}

func (self *ProductEditAPI) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var buf *bytes.Buffer = new(bytes.Buffer)
	var specid string = req.PostFormValue("id")
	var uuid UUID
	var codes *Barcodes = new(Barcodes)
	var mmap map[string]map[string][]*cassandra.Mutation
	var prod Product
	var mutations []*cassandra.Mutation
	var mutation *cassandra.Mutation
	var col *cassandra.Column
	var ire *cassandra.InvalidRequestException
	var ue *cassandra.UnavailableException
	var te *cassandra.TimedOutException
	var barcode string
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
	prod.Name = req.PostFormValue("prodname")
	if len(prod.Name) <= 0 {
		log.Print("Parsing product name: ", err)
		http.Error(w, "Product name empty", http.StatusNotAcceptable)
		return
	}

	prod.Price, err = strconv.ParseFloat(req.PostFormValue("price"), 64)
	if err != nil {
		log.Print("Parsing price: ", err)
		http.Error(w, "price: "+err.Error(), http.StatusNotAcceptable)
		return
	}

	prod.Stock, err = strconv.ParseUint(req.PostFormValue("stock"), 10, 64)
	if err != nil {
		log.Print("Parsing stock: ", err)
		http.Error(w, "stock: "+err.Error(), http.StatusNotAcceptable)
		return
	}

	// Check if the barcode has been given. If it was, it needs to be
	// numeric (EAN-13). If we find different types of barcodes we can
	// always revise this.
	for _, barcode = range req.PostForm["barcode"] {
		barcode = strings.Replace(barcode, " ", "", -1)
		if len(barcode) > 0 {
			match, err = regexp.MatchString("^[0-9]+$", barcode)
			if err != nil {
				productEditErrors.Add(err.Error(), 1)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if match {
				codes.Barcode = append(codes.Barcode, barcode)
			} else {
				productEditErrors.Add("barcode-format-error", 1)
				http.Error(w, "Barcode should only contain numbers",
					http.StatusNotAcceptable)
				return
			}
		}
	}

	// Create column data for the product row.
	col = cassandra.NewColumn()
	col.Name = []byte("name")
	col.Value = []byte(prod.Name)
	col.Timestamp = now.Unix()
	mutation = cassandra.NewMutation()
	mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
	mutation.ColumnOrSupercolumn.Column = col
	mutations = append(mutations, mutation)

	err = binary.Write(buf, binary.BigEndian, prod.Price)
	if err != nil {
		productEditErrors.Add(err.Error(), 1)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	col = cassandra.NewColumn()
	col.Name = []byte("price")
	col.Value = buf.Bytes()
	col.Timestamp = now.Unix()
	mutation = cassandra.NewMutation()
	mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
	mutation.ColumnOrSupercolumn.Column = col
	mutations = append(mutations, mutation)

	buf = new(bytes.Buffer)
	err = binary.Write(buf, binary.BigEndian, prod.Stock)
	if err != nil {
		productEditErrors.Add(err.Error(), 1)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	col = cassandra.NewColumn()
	col.Name = []byte("stock")
	col.Value = buf.Bytes()
	col.Timestamp = now.Unix()
	mutation = cassandra.NewMutation()
	mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
	mutation.ColumnOrSupercolumn.Column = col
	mutations = append(mutations, mutation)

	col = cassandra.NewColumn()
	col.Name = []byte("barcodes")
	col.Value, err = proto.Marshal(codes)
	if err != nil {
		productEditErrors.Add(err.Error(), 1)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	col.Timestamp = now.Unix()
	mutation = cassandra.NewMutation()
	mutation.ColumnOrSupercolumn = cassandra.NewColumnOrSuperColumn()
	mutation.ColumnOrSupercolumn.Column = col
	mutations = append(mutations, mutation)

	// If we're editing an existing product, re-use that UUID. Otherwise,
	// generate one.
	if len(specid) > 0 {
		uuid, err = ParseUUID(specid)
	} else {
		uuid, err = GenTimeUUID(&now)
	}
	if err != nil {
		productEditErrors.Add(err.Error(), 1)
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
	mmap[prod.Name] = make(map[string][]*cassandra.Mutation)
	mmap[prod.Name]["products_byname"] = mutations

	if len(barcode) > 0 {
		mmap[barcode] = make(map[string][]*cassandra.Mutation)
		mmap[barcode]["products_bybarcode"] = mutations
	}

	ire, ue, te, err = self.client.BatchMutate(mmap,
		cassandra.ConsistencyLevel_QUORUM)
	if ire != nil {
		log.Println("Invalid request: ", ire.Why)
		productEditErrors.Add(ire.Why, 1)
		return
	}
	if ue != nil {
		log.Println("Unavailable")
		productEditErrors.Add("unavailable", 1)
		return
	}
	if te != nil {
		log.Println("Request to database backend timed out")
		productEditErrors.Add("timeout", 1)
		return
	}
	if err != nil {
		log.Println("Generic error: ", err)
		productEditErrors.Add(err.Error(), 1)
		return
	}
}
