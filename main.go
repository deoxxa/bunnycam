package main // import "fknsrs.biz/p/bunnycam"

import (
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strconv"
	"time"

	"github.com/GeertJohan/go.rice"
	"github.com/Sirupsen/logrus"
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
	videoDevices   = app.Flag("video_device", "Device to use for video.").Default("/dev/video0").OverrideDefaultFromEnvar("VIDEO_DEVICE").ExistingFiles()
)

type imageUpdate struct {
	ID   int
	File string
	Time time.Time
}

func main() {
	kingpin.MustParse(app.Parse(os.Args[1:]))

	templateData, err := rice.MustFindBox("static").String("index.html")
	if err != nil {
		panic(err)
	}

	indexTemplate := template.Must(template.New("index").Parse(templateData))

	latestImage := make([]imageUpdate, len(*videoDevices))
	watchers := make(map[chan imageUpdate]bool)

	for id, videoDevice := range *videoDevices {
		thisDirectory := path.Join(*imageDirectory, fmt.Sprintf("cam%d", id))

		if err := os.Mkdir(thisDirectory, 0755); err != nil && !os.IsExist(err) {
			panic(err)
		}

		go func(id int, videoDevice, thisDirectory string) {
			for {
				logrus.WithFields(logrus.Fields{
					"id":        id,
					"device":    videoDevice,
					"directory": thisDirectory,
				}).Info("opening webcam")

				err := exec.Command(
					"streamer",
					"-c", videoDevice,
					"-t", "60",
					"-r", "1",
					"-o", path.Join(thisDirectory, fmt.Sprintf("snap_%d_00.jpeg", time.Now().Unix())),
				).Run()

				if err != nil {
					logrus.WithFields(logrus.Fields{
						"id":        id,
						"device":    videoDevice,
						"directory": thisDirectory,
						"error":     err.Error(),
					}).Info("streamer crashed")
				}

				time.Sleep(time.Second)
			}
		}(id, videoDevice, thisDirectory)

		go func(id int) {
			w, err := fsnotify.NewWatcher()
			if err != nil {
				panic(err)
			}
			if err := w.Watch(thisDirectory); err != nil {
				panic(err)
			}

			for ev := range w.Event {
				if ev.IsCreate() {
					latestImage[id] = imageUpdate{
						ID:   id,
						File: ev.Name,
						Time: time.Now(),
					}

					for c := range watchers {
						c <- latestImage[id]
					}
				}
			}
		}(id)
	}

	m := mux.NewRouter()

	m.Path("/").Methods("GET").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/html")
		w.WriteHeader(http.StatusOK)

		if err := indexTemplate.Execute(w, struct{ Cameras []imageUpdate }{latestImage}); err != nil {
			panic(err)
		}
	})

	m.Path("/reset").Methods("POST").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := exec.Command("killall", "streamer").Run(); err != nil {
			panic(err)
		}
	})

	m.Path("/latest/{id:[0-9]+}.jpeg").Methods("GET").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)

		var id int
		if idInt64, err := strconv.ParseInt(vars["id"], 10, 8); err != nil {
			panic(err)
		} else {
			id = int(idInt64)
		}

		if id >= len(*videoDevices) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		f, err := os.Open(latestImage[id].File)
		if err != nil {
			panic(err)
		}

		w.Header().Set("content-type", "image/jpeg")

		if _, err := io.Copy(w, f); err != nil {
			panic(err)
		}
	})

	m.Path("/stream").Methods("GET").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enc := eventsource.NewEncoder(w)

		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		c := make(chan imageUpdate, 10)

		watchers[c] = true
		defer delete(watchers, c)

		cn := w.(http.CloseNotifier).CloseNotify()

		for {
			select {
			case e, ok := <-c:
				if !ok {
					break
				}

				f, err := os.Open(e.File)
				if err != nil {
					panic(err)
				}

				d, err := ioutil.ReadAll(f)
				if err != nil {
					panic(err)
				}

				ev := eventsource.Event{
					Type: "image",
					Data: []byte(fmt.Sprintf(
						"%d::%s::%s",
						e.ID,
						e.Time.Format(time.RFC3339Nano),
						base64.StdEncoding.EncodeToString(d),
					)),
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

	n := negroni.New()

	n.Use(negroni.NewRecovery())
	n.Use(negronilogrus.NewMiddleware())
	n.UseHandler(m)

	if err := http.ListenAndServe(*addr, n); err != nil {
		panic(err)
	}
}
