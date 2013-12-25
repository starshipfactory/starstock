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
	"flag"
	"html/template"
	"log"
	"net/http"
	"os"
	"time"

	"ancient-solutions.com/ancientauth"
	"ancient-solutions.com/doozer/exportedservice"
)

func main() {
	var help bool
	var bindto, template_dir string
	var lockserv, lockboot, servicename string
	var ca, pub, priv, authserver string
	var requested_scope string
	var dbserver, keyspace string
	var searchif_tmpl *template.Template
	var permission_denied_tmpl *template.Template
	var exporter *exportedservice.ServiceExporter
	var authenticator *ancientauth.Authenticator
	var client *cassandra.RetryCassandraClient
	var ire *cassandra.InvalidRequestException
	var err error

	flag.BoolVar(&help, "help", false, "Display help")
	flag.StringVar(&bindto, "bind", "[::1]:8080",
		"The address to bind the web server to")
	flag.StringVar(&lockserv, "lockserver-uri",
		os.Getenv("DOOZER_URI"),
		"URI of a Doozer cluster to connect to")
	flag.StringVar(&ca, "cacert", "cacert.pem",
		"Path to the X.509 certificate of the certificate authority")
	flag.StringVar(&pub, "cert", "starstock.pem",
		"Path to the X.509 certificate")
	flag.StringVar(&priv, "key", "starstock.key",
		"Path to the X.509 private key file")
	flag.StringVar(&authserver, "auth-server",
		"login.ancient-solutions.com",
		"The server to send the user to")
	flag.StringVar(&template_dir, "template-dir", "",
		"Path to the directory with the HTML templates")
	flag.StringVar(&lockboot, "lockserver-boot-uri",
		os.Getenv("DOOZER_BOOT_URI"),
		"Boot URI to resolve the Doozer cluster name (if required)")
	flag.StringVar(&servicename, "service-name",
		"", "Service name to publish as to the lock server")
	flag.StringVar(&requested_scope, "scope",
		"staff", "People need to be in this scope to use the application")
	flag.StringVar(&dbserver, "cassandra-server", "localhost:9160",
		"Cassandra database server to use")
	flag.StringVar(&keyspace, "keyspace", "starstock",
		"Cassandra keyspace to use for accessing stock data")
	flag.Parse()

	if help {
		flag.Usage()
		os.Exit(1)
	}

	if len(template_dir) <= 0 {
		log.Fatal("The --template-dir flag must not be empty")
	}

	// Load and parse the HTML templates to be displayed.
	searchif_tmpl, err = template.ParseFiles(template_dir + "/search.tmpl")
	if err != nil {
		log.Fatal("Unable to parse search template: ", err)
	}

	// Load and parse the HTML templates to be displayed.
	permission_denied_tmpl, err = template.ParseFiles(template_dir +
		"/permission_denied.tmpl")
	if err != nil {
		log.Fatal("Unable to parse form template: ", err)
	}

	// Create the AncientAuth client
	authenticator, err = ancientauth.NewAuthenticator("StarStock", pub,
		priv, ca, authserver)
	if err != nil {
		log.Fatal("NewAuthenticator: ", err)
	}

	// Connect to the Cassandra server.
	client, err = cassandra.NewRetryCassandraClientTimeout(dbserver,
		10*time.Second)
	if err != nil {
		log.Fatal("Error opening connection to ", dbserver, ": ", err)
	}

	ire, err = client.SetKeyspace(keyspace)
	if ire != nil {
		log.Fatal("Error setting keyspace to ", keyspace, ": ", ire.Why)
	}
	if err != nil {
		log.Fatal("Error setting keyspace to ", keyspace, ": ", err)
	}

	// Register the URL handler to be invoked.
	http.Handle("/css/", http.FileServer(http.Dir(template_dir)))
	http.Handle("/js/", http.FileServer(http.Dir(template_dir)))
	http.Handle("/api/add-product", &ProductAddAPI{
		authenticator: authenticator,
		client:        client,
		scope:         requested_scope,
	})
	http.Handle("/api/products", &ProductSearchAPI{
		authenticator: authenticator,
		client:        client,
		scope:         requested_scope,
	})
	http.Handle("/", &ProductSearchForm{
		authenticator:        authenticator,
		scope:                requested_scope,
		searchifTmpl:         searchif_tmpl,
		permissionDeniedTmpl: permission_denied_tmpl,
	})

	// If a lock server was specified, attempt to use an anonymous port as
	// a Doozer exported HTTP service. Otherwise, just bind to the address
	// given in bindto, for debugging etc.
	if len(lockserv) > 0 {
		exporter, err = exportedservice.NewExporter(lockserv, lockboot)
		if err != nil {
			log.Fatal("doozer.DialUri ", lockserv, " (",
				lockboot, "): ", err)
		}

		defer exporter.UnexportPort()
		err = exporter.ListenAndServeNamedHTTP(servicename, bindto, nil)
		if err != nil {
			log.Fatal("ListenAndServe: ", err)
		}
	} else {
		err = http.ListenAndServe(bindto, nil)
		if err != nil {
			log.Fatal("ListenAndServe: ", err)
		}
	}
}
