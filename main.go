package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api"
	"github.com/joho/godotenv"
)

type key string

func main() {

	startTime := time.Now().Local().String()

	f, err := os.OpenFile("logs/"+startTime+".log", os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	log.SetOutput(f)

	// fetch env data
	var env map[string]string
	env, err = godotenv.Read()
	if err != nil {
		log.Fatal(err)
	}

	TOKEN_DB := env["INFLUXDB_TOKEN"]
	URL_DB := env["URL_DB"]
	ORG_NAME := env["ORG_NAME"]
	BUCKET_NAME := env["BUCKET_NAME"]

	client := influxdb2.NewClient(URL_DB, TOKEN_DB)
	defer client.Close()

	// use blocking (synchronous) api to write to db
	writeApi := client.WriteAPIBlocking(ORG_NAME, BUCKET_NAME)

	mux := http.NewServeMux()
	mux.HandleFunc("/", getRoot)
	mux.HandleFunc("/api", postSensorData)

	var db key = "db"
	var write key = "writeApi"

	ctx := context.Background()
	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
		BaseContext: func(_ net.Listener) context.Context {
			ctx = context.WithValue(ctx, db, client)
			ctx = context.WithValue(ctx, write, writeApi)
			return ctx
		},
	}

	log.Println("Server started on port 8080")
	err = server.ListenAndServe()

	if errors.Is(err, http.ErrServerClosed) {
		log.Println("Server closed under request")
	} else {
		log.Println("Server closed unexpectedly with error:")
		log.Fatal(err)
	}
}

func getRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.Error(w, "404 not found.", http.StatusNotFound)
		return
	}
	if r.Method != "GET" {
		http.Error(w, "Method is not supported.", http.StatusNotFound)
		return
	}
	w.Write([]byte("Welcome"))
}

func parseData(data string) (timestamp int64, hum float64, temp float64, x float64, y float64, z float64, err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("error parsing data")
		}
	}()

	log.Printf("incoming data: %s\n", data)
	bodyArr := strings.Split(data, "|")
	timestamp, _ = strconv.ParseInt(bodyArr[0], 10, 64)
	hum, _ = strconv.ParseFloat(bodyArr[1], 64)
	temp, _ = strconv.ParseFloat(bodyArr[2], 64)
	acc := strings.Split(bodyArr[3], ",")
	x, _ = strconv.ParseFloat(acc[0], 64)
	y, _ = strconv.ParseFloat(acc[1], 64)
	z, _ = strconv.ParseFloat(acc[2], 64)

	log.Printf("Timestamp: %d, Humidity: %f, Temperature: %f, Accelerometer: %f, %f, %f\n", timestamp, hum, temp, x, y, z)

	return timestamp, hum, temp, x, y, z, nil
}

func postSensorData(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api" {
		http.Error(w, "404 not found.", http.StatusNotFound)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "Method is not supported.", http.StatusNotFound)
		return
	}

	ctx := r.Context()
	writeApi := ctx.Value(key("writeApi")).(api.WriteAPIBlocking)

	log.Println(r.Header)

	var data, node string

	if err := r.ParseForm(); err != nil {
		if r.Body == nil {
			log.Printf("Error parsing form: %v", err)
			return
		}
		r.ParseMultipartForm(r.ContentLength)
		data = r.MultipartForm.Value["data"][0]
		node = r.MultipartForm.Value["node"][0]
	} else {
		data = r.FormValue("data")
		node = r.FormValue("node")
	}

	if node == "" {
		node = "unknown"
	}
	replacer := strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "", "\x00", "")

	data = replacer.Replace(data)

	var timestamp, hum, temp, x, y, z, err = parseData(data)

	if err != nil {
		log.Printf("Error: %s\n", err)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("400 - Bad request data"))
		return
	}

	p1 := influxdb2.NewPointWithMeasurement("air").
		AddTag("location", node).
		AddField("humidity", hum).
		AddField("temperature", temp).
		SetTime(time.Unix(timestamp, 0))

	p2 := influxdb2.NewPointWithMeasurement("accelerometer").
		AddTag("location", node).
		AddField("x", x).
		AddField("y", y).
		AddField("z", z).
		SetTime(time.Unix(timestamp, 0))

	if err := writeApi.WritePoint(context.Background(), p1, p2); err != nil {
		log.Println(err)
	}

	if msg, err := json.Marshal(map[string]string{"status": "ok"}); err != nil {
		log.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("500 - Something bad happened!"))
	} else {
		w.Write(msg)
	}
}
