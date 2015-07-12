package main // import "fknsrs.biz/p/bunnycam"

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"sort"
	"time"

	"gopkg.in/alecthomas/kingpin.v2"
)

var (
	app            = kingpin.New("bunnycam", "Take pictures of a rabbit. Enjoy them.")
	imageDirectory = app.Flag("images", "Where to read images from.").Default("/var/lib/bunnycam/data").OverrideDefaultFromEnvar("IMAGE_DIRECTORY").ExistingDir()
	addr           = app.Flag("addr", "Address to listen on.").Default(":3000").OverrideDefaultFromEnvar("ADDR").String()
)

func main() {
	kingpin.MustParse(app.Parse(os.Args[1:]))

	go func() {
		for {
			exec.Command(
				"streamer",
				"-t", "99999999",
				"-r", "1",
				"-o", path.Join(*imageDirectory, fmt.Sprintf("snap_%d_00000000.jpeg", time.Now().Unix())),
			).Run()

			time.Sleep(time.Second)
		}
	}()

	m := http.NewServeMux()

	m.HandleFunc("/latest.jpeg", func(w http.ResponseWriter, r *http.Request) {
		d, err := os.Open(*imageDirectory)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer d.Close()

		names, err := d.Readdirnames(0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		sort.Strings(names)

		latest := names[len(names)-1]

		f, err := os.Open(path.Join(*imageDirectory, latest))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("content-type", "image/jpeg")
		w.Header().Set("refresh", "1")

		if _, err := io.Copy(w, f); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	})

	if err := http.ListenAndServe(*addr, m); err != nil {
		panic(err)
	}
}
