package tryfiles

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"
)

func New() *Handler {
	return &Handler{
		trys: []string{"/index.html"},
		next: http.HandlerFunc(http.NotFound),
	}
}

type Handler struct {
	fs   http.FileSystem
	trys []string
	next http.Handler
	rMod func(*http.Request) *http.Request
}

func (h *Handler) HTTPFs(fs http.FileSystem, trys ...string) *Handler {
	h.fs = fs
	if len(trys) > 0 && trys[0] != "" {
		h.trys = trys
	}
	return h
}

func (h *Handler) Fs(fs fs.FS, trys ...string) *Handler {
	return h.HTTPFs(http.FS(fs))
}

func (h *Handler) Try(trys ...string) *Handler {
	h.trys = trys
	return h
}

func (h *Handler) NotFound(notFound http.Handler) *Handler {
	h.next = notFound
	return h
}

func (h *Handler) RMod(rMod func(*http.Request) *http.Request) *Handler {
	h.rMod = rMod
	return h
}

func (h *Handler) Strip(prefix string) *Handler {
	return h.RMod(func(r *http.Request) *http.Request {
		if prefix != "" {
			if p := strings.TrimPrefix(r.URL.Path, prefix); p != r.URL.Path {
				r.URL.Path = p
			}
			if p := strings.TrimPrefix(r.URL.RawPath, prefix); p != r.URL.RawPath {
				r.URL.RawPath = p
			}
		}
		return r
	})
}

func (h *Handler) Strips(suffix string) *Handler {
	return h.RMod(func(r *http.Request) *http.Request {
		if suffix != "" {
			if p := strings.TrimSuffix(r.URL.Path, suffix); p != r.URL.Path {
				r.URL.Path = p
			}
			if p := strings.TrimSuffix(r.URL.RawPath, suffix); p != r.URL.RawPath {
				r.URL.RawPath = p
			}
		}
		return r
	})
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.rMod != nil {
		r = h.rMod(r)
	}

	if h.fs == nil {
		h.next.ServeHTTP(w, r)
	}

	n := wGet(w)
	defer wPut(n)
	if http.FileServer(h.fs).ServeHTTP(n, r); n.status == 404 {
		w.Header().Del("Content-Type")
		w.Header().Del("X-Content-Type-Handler")
		for _, name := range h.trys {
			if h.tryFile(w, r, name) {
				return
			}
		}
		h.next.ServeHTTP(w, r)
	}
}

func (h *Handler) tryFile(w http.ResponseWriter, r *http.Request, name string) bool {
	var status int
	f, err := h.fs.Open(name)
	if err != nil {
		if status = h.toHTTPError(err); status == 404 {
			return false
		}

		return h.statusRespond(w, status)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		if status = h.toHTTPError(err); status == 404 {
			return false
		}
		return h.statusRespond(w, status)
	}

	http.ServeContent(w, r, path.Base(name), stat.ModTime(), f)
	return true
}

func (h *Handler) toHTTPError(err error) (httpStatus int) {
	if errors.Is(err, fs.ErrNotExist) {
		return http.StatusNotFound
	}
	if errors.Is(err, fs.ErrPermission) {
		return http.StatusForbidden
	}
	return http.StatusInternalServerError
}

func (h *Handler) statusRespond(w http.ResponseWriter, status int) bool {
	http.Error(w, fmt.Sprintf("%d %s", status, strings.ToLower(http.StatusText(status))), status)
	return true
}

type no404w struct {
	http.ResponseWriter
	status int
}

func (w *no404w) WriteHeader(statusCode int) {
	w.status = statusCode
	if statusCode != 404 {
		w.ResponseWriter.WriteHeader(statusCode)
	}
}

func (w no404w) Write(data []byte) (n int, err error) {
	if w.status == 404 {
		return
	}
	return w.ResponseWriter.Write(data)
}

var wPool = &sync.Pool{New: func() any { return &no404w{} }}

func InitPool(size int) {
	for i := 0; i < size; i++ {
		wPut(wGet(nil))
	}
}

func wGet(w http.ResponseWriter) (n *no404w) {
	if n, _ = w.(*no404w); n == nil {
		n = wPool.Get().(*no404w)
		n.ResponseWriter = w
	}
	return
}

func wPut(w *no404w) {
	w.ResponseWriter = nil
	w.status = 0
	wPool.Put(w)
}
