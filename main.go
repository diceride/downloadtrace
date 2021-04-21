package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"reflect"
	"time"
	"unsafe"

	"cloud.google.com/go/datastore"
	"cloud.google.com/go/storage"
)

type DownloadLogEntry struct {
	Country string
	Region string
	DownloadedAt time.Time
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
		log.Printf("Defaulting to port %s", port)
	}

	http.HandleFunc("/", indexHandler)

	log.Printf("Listening on port %s", port)

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache, private, max-age=0")
	w.Header().Set("Expires", time.Unix(0, 0).Format(http.TimeFormat))
	w.Header().Set("Pragma", "no-cache")

	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	} else if r.Method != "GET" {
		http.Error(w, "405 method not allowed", http.StatusMethodNotAllowed)
		return
	}

	filename := r.URL.Query().Get("file")
	if filename == "" {
		http.Error(w, "400 bad request: empty filename", http.StatusBadRequest)
		return
	} else if len(filename) > 128 {
		http.Error(w, "400 bad request: invalid filename", http.StatusBadRequest)
		return
	}

	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")

	// Create a new cloud datastore client
	datastoreClient, err := datastore.NewClient(r.Context(), projectID)
	if err != nil {
		// Cloud logging
		log.Printf("datastore.NewClient: %v", err)

		http.Error(w, "500 internal error", http.StatusInternalServerError)
		return
	}
	defer datastoreClient.Close()

	// Create a new cloud datastore key
	filenameKey := datastore.NameKey("DownloadLogEntry", filename, nil)

	var empty DownloadLogEntry

	// Determine whether the cloud datastore key exists
	err = datastoreClient.Get(r.Context(), filenameKey, &empty)
	if err != nil {
		if err != datastore.ErrNoSuchEntity {
			// Cloud logging
			log.Printf("datastoreClient.Get: %v", err)

			http.Error(w, "500 internal error", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "403 forbidden", http.StatusForbidden)
		return
	}

	bucketName := os.Getenv("BUCKET_NAME")

	// Create a new cloud storage client
	storageClient, err := storage.NewClient(r.Context())
	if err != nil {
		// Cloud logging
		log.Printf("storage.NewClient: %v", err)

		http.Error(w, "500 internal error", http.StatusInternalServerError)
		return
	}
	defer storageClient.Close()

	// Determine whether the storage bucket object exists
	_, err = storageClient.Bucket(bucketName).Object(filename).Attrs(r.Context())
	if err != nil {
		if err == storage.ErrObjectNotExist {
			http.NotFound(w, r)
			return
		}

		// Cloud logging
		log.Printf("storageClient.Bucket: %v", err)

		http.Error(w, "500 internal error", http.StatusInternalServerError)
		return
	}

	// Run an atomic cloud datastore transaction
	_, err = datastoreClient.RunInTransaction(r.Context(), func(tx *datastore.Transaction) error {
		var empty DownloadLogEntry
		if err := tx.Get(filenameKey, &empty); err != datastore.ErrNoSuchEntity {
			return err
		}

		var entry = DownloadLogEntry{
			// https://cloud.google.com/appengine/docs/standard/go/reference/request-response-headers
			Country: r.Header.Get("X-Appengine-Country"),
			Region: r.Header.Get("X-Appengine-Region"),

			DownloadedAt: time.Now(),
		}

		_, err := tx.Put(filenameKey, &entry)
		return err
	})
	if err != nil {
		// Cloud logging
		log.Printf("datastoreClient.RunInTransaction: %v", err)

		http.Error(w, "500 internal error", http.StatusInternalServerError)
		return
	}

	datastoreClient.Close()

	fileURL, err := url.Parse("https://storage.googleapis.com/" + bucketName + "/" + filename)
	if err != nil {
		// Cloud logging
		log.Printf("url.Parse: %v", err)

		http.Error(w, "500 internal error", http.StatusInternalServerError)
		return
	}

	// Reverse proxy the request from the client to cloud storage
	(&httputil.ReverseProxy{
		// TODO: unsafe
		Transport: (*http.Client)(unsafe.Pointer(reflect.ValueOf(storageClient).Elem().FieldByName("hc").Pointer())).Transport,
		Director: func(r *http.Request) {
			r.URL = fileURL
			r.Host = fileURL.Host
		},
		ModifyResponse: func(r *http.Response) error {
			// Remove cloud storage default headers
			r.Header.Del("expires")
			r.Header.Del("x-goog-generation")
			r.Header.Del("x-goog-hash")
			r.Header.Del("x-goog-metageneration")
			r.Header.Del("x-goog-storage-class")
			r.Header.Del("x-goog-stored-content-encoding")
			r.Header.Del("x-goog-stored-content-length")
			r.Header.Del("x-guploader-uploadid")
			return nil
		},
	}).ServeHTTP(w, r)
}
