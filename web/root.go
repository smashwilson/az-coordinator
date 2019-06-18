package web

import "net/http"

func (s Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	s.cors(w, r, methodHandlerMap{
		http.MethodGet: func() { w.Write([]byte("ok")) },
	})
}
