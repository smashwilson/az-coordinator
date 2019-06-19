package web

import "net/http"

func (s Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Not found"))
		return
	}

	s.cors(w, r, methodHandlerMap{
		http.MethodGet: func() { w.Write([]byte("ok")) },
	})
}
