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
	"database/cassandra"
	"encoding/json"
	"expvar"
	"fmt"
	"html/template"
	"log"
	"net/http"

	"ancient-solutions.com/ancientauth"
)

// Individual search results.
type SearchResult struct {
	Name    string
	Picture string
	Path    string
}

// Search results with different categories of results.
// They are separated so they can be grouped together.
type CategorizedSearchResult struct {
	Products []*SearchResult
	Vendors  []*SearchResult
}

// Total number of requests received.
var numRequests *expvar.Int = expvar.NewInt("num-http-requests")

// Total number of requests to the API.
var numAPIRequests *expvar.Int = expvar.NewInt("num-api-requests")

// Number of requests which didn't fit the requested scope.
var numDisallowedScope *expvar.Int = expvar.NewInt("num-requests-rejected-for-scope")

// Number of JSON marshalling errors.
var numJSONMarshalErrors *expvar.Int = expvar.NewInt("num-json-marshalling-errors")

// Map of HTTP write errors by type.
var numHTTPWriteErrors *expvar.Map = expvar.NewMap("num-http-write-errors")

// Map of Cassandra errors by type.
var numCassandraErrors *expvar.Map = expvar.NewMap("num-cassandra-errors")

type ProductSearchForm struct {
	authenticator        *ancientauth.Authenticator
	permissionDeniedTmpl *template.Template
	searchifTmpl         *template.Template
	scope                string
}

type ProductSearchAPI struct {
	authenticator *ancientauth.Authenticator
	client        *cassandra.RetryCassandraClient
	scope         string
}

func UUID2String(uuid []byte) string {
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

func (self *ProductSearchForm) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var err error
	numRequests.Add(1)

	if self.authenticator.GetAuthenticatedUser(req) == "" {
		self.authenticator.RequestAuthorization(w, req)
		return
	}

	// Check the user is in the reqeuested scope.
	if !self.authenticator.IsAuthenticatedScope(req, self.scope) {
		numDisallowedScope.Add(1)
		w.WriteHeader(http.StatusForbidden)
		err = self.permissionDeniedTmpl.Execute(w, self.scope)
		if err != nil {
			log.Print("Error executing permission denied template: ", err)
		}
		return
	}

	// Otherwise, we simply serve the search template.
	err = self.searchifTmpl.Execute(w, nil)
	if err != nil {
		log.Print("Error executing permission denied template: ", err)
	}
}

func (self *ProductSearchAPI) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var err error
	var query string = req.FormValue("q")
	var rawdata []byte
	var res CategorizedSearchResult

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

	if len(query) >= 3 {
		var cp *cassandra.ColumnParent = cassandra.NewColumnParent()
		var pred *cassandra.SlicePredicate = cassandra.NewSlicePredicate()
		var kr *cassandra.KeyRange = cassandra.NewKeyRange()
		var slices []*cassandra.KeySlice
		var ire *cassandra.InvalidRequestException
		var ue *cassandra.UnavailableException
		var te *cassandra.TimedOutException
		var endkey []byte = []byte(query)
		var r *SearchResult
		var pos int = len(endkey) - 1

		// Handle overflows
		for endkey[pos] == 255 && pos > 0 {
			endkey[pos] = 0
			pos--
		}

		// Produce endkey by incrementing the last byte of the start key.
		endkey[pos]++

		// Search for projects with the given name.
		cp.ColumnFamily = "products_byname"
		pred.ColumnNames = [][]byte{[]byte("product")}

		kr.StartKey = []byte(query)
		kr.EndKey = endkey
		kr.Count = 32 // Limit to 32 results.

		slices, ire, ue, te, err = self.client.GetRangeSlices(
			cp, pred, kr, cassandra.ConsistencyLevel_ONE)
		if ire != nil {
			log.Print("Error fetching products_byname: ", ire.Why)
			numCassandraErrors.Add("invalid-request", 1)
			http.Error(w, ire.Why, http.StatusInternalServerError)
			return
		}
		if ue != nil {
			log.Print("Cassandra unavailable when fetching products_byname")
			numCassandraErrors.Add("invalid-request", 1)
			http.Error(w, "Database unavailable", http.StatusInternalServerError)
			return
		}
		if te != nil {
			log.Print("Cassandra timed out when fetching products_byname")
			numCassandraErrors.Add("timeout", 1)
			http.Error(w, "Database timed out", http.StatusInternalServerError)
			return
		}
		if err != nil {
			log.Print("OS error when fetching products_byname: ", err)
			numCassandraErrors.Add("generic-error", 1)
			http.Error(w, "OS error talking to database",
				http.StatusInternalServerError)
			return
		}

		for _, slice := range slices {
			for _, csc := range slice.Columns {
				var col *cassandra.Column = csc.Column
				if col == nil || !col.IsSetValue() {
					continue
				}

				if string(col.Name) != "product" {
					log.Print("Bizarre products_byname row ",
						string(slice.Key), " (", slice.Key, "), has ",
						string(col.Name), " (", col.Name, ")")
					continue
				}

				r = new(SearchResult)
				r.Name = string(slice.Key)
				r.Path = "/product/" + UUID2String(col.Value)
				res.Products = append(res.Products, r)
			}
		}

		// TODO(caoimhe): stub
		r = new(SearchResult)
		r.Name = "ACME Inc."
		r.Path = "/vendor/acme"
		res.Vendors = append(res.Vendors, r)

		r = new(SearchResult)
		r.Name = "Starship Factory"
		r.Path = "/vendor/starshipfactory"
		res.Vendors = append(res.Vendors, r)

		r = new(SearchResult)
		r.Name = "RaumZeitLabor e.V."
		r.Path = "/vendor/rzl"
		res.Vendors = append(res.Vendors, r)

		r = new(SearchResult)
		r.Name = "Doctor in the TARDIS"
		r.Path = "/vendor/doctor"
		res.Vendors = append(res.Vendors, r)
	}

	rawdata, err = json.Marshal(res)
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
