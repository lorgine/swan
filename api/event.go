package api

import (
	"net/http"
	"strings"

	log "github.com/Sirupsen/logrus"
)

func (r *Router) events(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(nil)

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	if err := req.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if catchUp := req.Form.Get("catchUp"); strings.ToLower(catchUp) == "true" {
		go func() {
			for _, ev := range r.driver.TaskEvents() {
				if _, err := w.Write(ev.Format()); err != nil {
					log.Errorf("write event message to client [%s] error: [%v]", req.RemoteAddr, err)
				}
			}
		}()
	}

	if err := r.driver.SubscribeEvent(w, req.RemoteAddr); err != nil {
		http.Error(w, err.Error(), http.StatusMethodNotAllowed)
		return
	}

	return
}
