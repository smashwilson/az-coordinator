package web

import "net/http"

func (s Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
}
