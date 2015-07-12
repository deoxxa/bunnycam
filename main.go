package main // import "fknsrs.biz/p/bunnycam"

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"time"
)

func snapshot() (string, error) {
	f := path.Join(os.TempDir(), fmt.Sprintf("%d.jpeg", time.Now().UnixNano()))

	cmd := exec.Command("streamer", "-o", f)

	if err := cmd.Run(); err != nil {
		return f, err
	}

	return f, nil
}

func main() {
	m := http.NewServeMux()

	m.HandleFunc("/snapshot.jpeg", func(w http.ResponseWriter, r *http.Request) {
		fn, err := snapshot()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		f, err := os.Open(fn)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("content-type", "image/jpeg")

		if _, err := io.Copy(w, f); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	})

	if err := http.ListenAndServe(":3000", m); err != nil {
		panic(err)
	}
}
