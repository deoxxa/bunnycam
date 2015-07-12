package main // import "fknsrs.biz/p/bunnycam"

import (
	"encoding/base64"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path"
	"time"

	"github.com/GeertJohan/go.rice"
	"github.com/bernerdschaefer/eventsource"
	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"github.com/howeyc/fsnotify"
	"github.com/meatballhat/negroni-logrus"
	"gopkg.in/alecthomas/kingpin.v2"
)

var (
	app            = kingpin.New("bunnycam", "Take pictures of a rabbit. Enjoy them.")
	imageDirectory = app.Flag("images", "Where to read images from.").Default("/var/lib/bunnycam/data").OverrideDefaultFromEnvar("IMAGE_DIRECTORY").ExistingDir()
	addr           = app.Flag("addr", "Address to listen on.").Default(":3000").OverrideDefaultFromEnvar("ADDR").String()
)

func main() {
	kingpin.MustParse(app.Parse(os.Args[1:]))

	w, err := fsnotify.NewWatcher()
	if err != nil {
		panic(err)
	}
	if err := w.Watch(*imageDirectory); err != nil {
		panic(err)
	}

	var latestImage string
	watchers := make(map[chan string]bool)

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

	go func() {
		for ev := range w.Event {
			if ev.IsCreate() {
				latestImage = ev.Name

				for c := range watchers {
					c <- latestImage
				}
			}
		}
	}()

	m := mux.NewRouter()

	m.Path("/latest.jpeg").Methods("GET").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, err := os.Open(latestImage)
		if err != nil {
			panic(err)
		}

		w.Header().Set("content-type", "image/jpeg")
		w.Header().Set("refresh", "1")

		if _, err := io.Copy(w, f); err != nil {
			panic(err)
		}
	})

	m.Path("/stream.mjpeg").Methods("GET").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "video/x-motion-jpeg")
		w.WriteHeader(http.StatusOK)

		c := make(chan string, 10)
		c <- latestImage

		watchers[c] = true
		defer delete(watchers, c)

		cn := w.(http.CloseNotifier).CloseNotify()

		for {
			select {
			case n, ok := <-c:
				if !ok {
					break
				}

				f, err := os.Open(n)
				if err != nil {
					panic(err)
				}

				if _, err := io.Copy(w, f); err != nil {
					panic(err)
				}

				w.(http.Flusher).Flush()
			case <-cn:
				return
			}
		}
	})

	m.Path("/stream.hack").Methods("GET").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enc := eventsource.NewEncoder(w)

		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		c := make(chan string, 10)
		c <- latestImage

		watchers[c] = true
		defer delete(watchers, c)

		cn := w.(http.CloseNotifier).CloseNotify()

		for {
			select {
			case n, ok := <-c:
				if !ok {
					break
				}

				f, err := os.Open(n)
				if err != nil {
					panic(err)
				}

				d, err := ioutil.ReadAll(f)
				if err != nil {
					panic(err)
				}

				ev := eventsource.Event{
					Type: "image",
					Data: []byte(base64.StdEncoding.EncodeToString(d)),
				}

				if err := enc.Encode(ev); err != nil {
					panic(err)
				}

				if err := enc.Flush(); err != nil {
					panic(err)
				}
			case <-cn:
				return
			case <-time.After(time.Second * 30):
				if err := enc.WriteField("heartbeat", nil); err != nil {
					panic(err)
				}
			}
		}
	})

	m.NotFoundHandler = http.FileServer(rice.MustFindBox("static").HTTPBox())

	n := negroni.New()

	n.Use(negroni.NewRecovery())
	n.Use(negronilogrus.NewMiddleware())
	n.UseHandler(m)

	if err := http.ListenAndServe(*addr, n); err != nil {
		panic(err)
	}
}
