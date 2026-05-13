package server

import (
	"io"
	"net/http"
)

func ioCopy(w http.ResponseWriter, r io.Reader) (int64, error) {
	return io.Copy(w, r)
}
