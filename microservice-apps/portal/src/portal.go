package main

import (
	"encoding/json"
	"errors"
	"github.com/gorilla/mux"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
)

// Spec represents the ErrorResponse response object.
type ErrorResponse struct {
	Error string `json:"error"`
}

func main() {
	r := mux.NewRouter()
	r.HandleFunc("/time/{format}", TimeHandler).Methods("GET")
	r.HandleFunc("/calc/{formula}", CalcHandler).Methods("GET")
	r.PathPrefix("/").Handler(http.FileServer(http.Dir(GetHtmlFileDir())))
	http.Handle("/", r)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// Handler to process the GET HTTP request. Calls the Time service
func TimeHandler(res http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	format := vars["format"]
	log.Printf("Time format is %s\n", format)

	var time_endpoint string
	var err error
	if time_endpoint, err = GetTimeEndpoint(); err != nil {
		log.Fatal(err)
	}
	url := "http://" + time_endpoint + "/time/" + format
	log.Printf("URL is %v\n", url)

	user, pass := GetTimeCredentials()
	log.Printf("Got credentials username= %s, password=%s\n", user, pass)
	req, err = http.NewRequest("GET", url, nil)
	if len(user) > 0 && len(pass) > 0 {
		req.SetBasicAuth(user, pass)
	}
	cli := &http.Client{}
	response, err := cli.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode != 200 {
		log.Printf("Got Error response: %s\n", response.Status)
		errorMsg := ErrorResponse{Error: response.Status}
		if err := json.NewEncoder(res).Encode(errorMsg); err != nil {
			log.Panic(err)
		}
	} else {
		contents, err := ioutil.ReadAll(response.Body)
		if err != nil {
			log.Fatal(err)
		}

		log.Printf("Got Successful response: %s\n", string(contents))
		res.Write(contents)
	}
}

// Handler to process the GET HTTP request. Calls the Calc service
func CalcHandler(res http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	formula := vars["formula"]
	log.Printf("Formula is %s\n", formula)

	var calc_endpoint string
	var err error
	if calc_endpoint, err = GetCalcEndpoint(); err != nil {
		log.Fatal(err)
	}
	url := "http://" + calc_endpoint + "/calc/" + formula
	log.Printf("URL is %v\n", url)

	user, pass := GetCalcCredentials()
	log.Printf("Got credentials username= %s, password=%s\n", user, pass)
	req, err = http.NewRequest("GET", url, nil)

	if len(user) > 0 && len(pass) > 0 {
		req.SetBasicAuth(user, pass)
	}
	cli := &http.Client{}
	response, err := cli.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode != 200 {
		log.Printf("Got error response: %s\n", response.Status)
		errorMsg := ErrorResponse{Error: response.Status}
		if err := json.NewEncoder(res).Encode(errorMsg); err != nil {
			log.Panic(err)
		}
	} else {
		contents, err := ioutil.ReadAll(response.Body)
		if err != nil {
			log.Fatal(err)
		}

		log.Printf("Got successful response: %s\n", string(contents))
		res.Write(contents)
	}
}

// get the time service endpoint from DNS
func GetTimeEndpoint() (string, error) {
	var addrs []*net.SRV
	var err error
	if _, addrs, err = net.LookupSRV("time", "tcp", "servicediscovery.internal"); err != nil {
		return "", err
	}
	for _, addr := range addrs {
		return addr.Target + ":" + strconv.Itoa(int(addr.Port)), nil
	}
	return "", errors.New("No record found")
}

// get the calc service endpoint from DNS
func GetCalcEndpoint() (string, error) {
	var addrs []*net.SRV
	var err error
	if _, addrs, err = net.LookupSRV("calc", "tcp", "servicediscovery.internal"); err != nil {
		return "", err
	}
	for _, addr := range addrs {
		return addr.Target + ":" + strconv.Itoa(int(addr.Port)), nil
	}
	return "", errors.New("No record found")
}

// get the time service credentials
func GetTimeCredentials() (string, string) {
	stock_user := os.Getenv("TIME_USERNAME")
	stock_pass := os.Getenv("TIME_PASSWORD")
	if len(stock_user) > 0 && len(stock_pass) > 0 {
		return stock_user, stock_pass
	}
	log.Println("env variables TIME_USERNAME & TIME_PASSWORD not found. Returning default values: admin/password.")
	return "admin", "password"
}

// get the calc service credentials
func GetCalcCredentials() (string, string) {
	weather_user := os.Getenv("CALC_USERNAME")
	weather_pass := os.Getenv("CALC_PASSWORD")
	if len(weather_user) > 0 && len(weather_pass) > 0 {
		return weather_user, weather_pass
	}
	log.Println("env variables CALC_USERNAME & CALC_PASSWORD not found. Returning default values: admin/password.")
	return "admin", "password"
}

// get the HTML file
func GetHtmlFileDir() string {
	html_file_dir := os.Getenv("HTML_FILE_DIR")
	if len(html_file_dir) > 0 {
		return html_file_dir
	}
	log.Println("env variables HTML_FILE_DIR not found. Returning default values: ./public/")
	return "./public/"
}
