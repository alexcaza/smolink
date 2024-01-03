package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/lithammer/shortuuid/v4"
	_ "github.com/mattn/go-sqlite3"
)

type newURLResponse struct {
	Url      string `json:"url"`
	Shorturl string `json:"shorturl"`
}

type shortURL struct {
	Value string
}

type db struct {
	Connection *sql.DB
}

func (d db) validateAuthorization(token string) bool {
	var dbToken string
	row := d.Connection.QueryRow("select key from authorization where key = ?", token)
	err := row.Scan(&dbToken)

	if err != nil {
		log.Println("Failed to get token... Are you sure you're authorized? Token:", token)
		return false
	}

	return token == dbToken
}

func (d db) saveShortUrl(originalUrl string) (shortURL, error) {
	key := shortuuid.New()
	short := mkShortURL(key)
	_, err := d.Connection.Exec("insert into links (short_url, full_url, date_created) values(?, ?, ?)", key, originalUrl, time.Now())
	return short, err
}

func (d db) getFullUrl(shortUrl shortURL) (string, error) {
	var fullUrl string
	pathWithLeadingSlash, err := url.ParseRequestURI(shortUrl.Value)
	path := strings.ReplaceAll(pathWithLeadingSlash.Path, "/", "")
	if err != nil {
		return "", err
	}
	row := d.Connection.QueryRow("select full_url from links where short_url = ?", path)
	err = row.Scan(&fullUrl)

	return fullUrl, err
}

func (d db) generateDefaultAuthorizationToken() (string, error) {
	var existingKey string
	row := d.Connection.QueryRow("select key from authorization")
	err := row.Scan(&existingKey)

	// Scan throws err on empty; exit without creating new if
	// a row is present already
	if err == nil {
		return "", err
	}

	key := uuid.New().String()
	_, err = d.Connection.Exec("insert into authorization (id, key, date_created) values(?, ?, ?)", nil, key, time.Now())
	return key, err
}

func baseURL() string {
	return os.Getenv("BASE_URL")
}

func mkShortURL(path string) shortURL {
	return shortURL{baseURL() + "/" + path}
}

func createLinkHandler(db db) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if len(authHeader) < 1 || !strings.Contains(authHeader, "Bearer ") {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		token := strings.Split(authHeader, "Bearer ")[1]
		isAuthed := db.validateAuthorization(token)

		if !isAuthed {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		switch r.Method {
		case http.MethodPost, http.MethodGet:
			values := r.URL.Query()
			log.Println("URL given: ", values.Get("url"))
			url, err := url.ParseRequestURI(values.Get("url"))
			if err != nil {
				log.Println("Malformed URL: ", err)
				http.Error(w, "Malformed URL", http.StatusBadRequest)
				return
			}

			shortURL, err := db.saveShortUrl(url.String())
			if err != nil {
				log.Println("Creating short url failed with: ", err)
				http.Error(w, "Failed to create short url", http.StatusInternalServerError)
				return
			}

			response := newURLResponse{Url: shortURL.Value, Shorturl: shortURL.Value}
			fmt.Println(url, response)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		default:
			http.Error(w, "Method not supported", http.StatusMethodNotAllowed)
		}
	}
}

func expandLinkHandler(db db) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			path := shortURL{r.URL.Path}
			fullUrl, err := db.getFullUrl(path)
			if err != nil {
				log.Println("Failed to find full URL: ", err)
				http.Error(w, "Failed to get full url", http.StatusInternalServerError)
				return
			}

			http.Redirect(w, r, fullUrl, http.StatusTemporaryRedirect)
		default:
			http.Error(w, "Method not supported", http.StatusMethodNotAllowed)
		}
	}
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatalln("Error loading .env file.")
	}

	_db, err := sql.Open("sqlite3", "./smolink.db")
	defer _db.Close()
	db := db{_db}
	_, err = db.Connection.Exec("create table if not exists authorization (id integer PRIMARY KEY, key text NOT NULL, date_created INTEGER NOT NULL)")
	if err != nil {
		log.Println("Failed to created table with error:", err)
	}

	// Create default authorization key if none present
	key, err := db.generateDefaultAuthorizationToken()

	if err != nil {
		log.Fatalln("Couldn't generate default auth key with: ", err)
	}

	if len(key) > 0 {
		log.Println("Default authorization key:", key)
	}

	_, err = db.Connection.Exec("create table if not exists links (short_url text NOT NULL PRIMARY KEY, full_url text NOT NULL, date_created INTEGER NOT NULL)")
	if err != nil {
		log.Println("Failed to created table with error:", err)
	}

	http.HandleFunc("/", expandLinkHandler(db))
	http.HandleFunc("/c", createLinkHandler(db))

	log.Println("Running at", os.Getenv("BASE_URL"))
	log.Fatalln(http.ListenAndServe(":"+os.Getenv("PORT"), nil))
}
