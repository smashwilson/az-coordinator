package main

import (
	"log"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
)

func getSetting(varName string, defaultValue string) string {
	if value, ok := os.LookupEnv(varName); ok {
		return value
	} else {
		return defaultValue
	}
}

func main() {
	usr, err := user.Current()
	if err != nil {
		log.Fatal(err)
	}

	addr := getSetting("AZ_ADDRESS", ":8443")
	certFile := getSetting("AZ_CERTIFICATE", filepath.Join(usr.HomeDir, ".secrets", "certificate.pem"))
	keyFile := getSetting("AZ_KEY", filepath.Join(usr.HomeDir, ".secrets", "key.pem"))
	token := getSetting("AZ_TOKEN", "")

	if token == "" {
		log.Fatal("AZ_TOKEN must be provided.")
	}

	http.HandleFunc("/", handleRoot)
	http.HandleFunc("/status", protected(token, handleStatus))
	http.HandleFunc("/update", protected(token, handleUpdate))

	log.Printf("Serving on address: %s\n", addr)

	log.Fatal(http.ListenAndServeTLS(addr, certFile, keyFile, nil))
}

func protected(token string, handler func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, password, ok := r.BasicAuth(); !ok || password != token {
			w.WriteHeader(401)
			w.Write([]byte("Unauthorized"))
		}

		handler(w, r)
	}
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	//
}

func handleUpdate(w http.ResponseWriter, r *http.Request) {
	//
}
