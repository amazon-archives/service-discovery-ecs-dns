package main

import (
	"encoding/json"
	"github.com/goji/httpauth"
	"github.com/gorilla/mux"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Spec represents the Time response object.
type TimeResponse struct {
	Value       string `json:"value"`
	ContainerId string `json:"containerid"`
	InstanceId  string `json:"instanceid"`
}

// Spec represents the Healthcheck response object.
type Healthcheck struct {
	Status string `json:"status"`
}

func main() {
	r := mux.NewRouter()
	r.HandleFunc("/time/{format}", TimeHandler).Methods("GET")
	http.HandleFunc("/health", HealthHandler)
	http.Handle("/", (httpauth.SimpleBasicAuth(GetHttpUsername(), GetHttpPassord()))(r))
	log.Fatal(http.ListenAndServe(":8081", nil))
}

// Handler to process the healthcheck
func HealthHandler(res http.ResponseWriter, req *http.Request) {
	hc := Healthcheck{Status: "OK"}
	if err := json.NewEncoder(res).Encode(hc); err != nil {
		log.Panic(err)
	}
}

// Handler to process the GET HTTP request. Returns the time formatted
func TimeHandler(res http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	format := vars["format"]
	log.Printf("Time format is %s\n", format)

	timeValue := time.Now().Format(format)

	timeReponse := TimeResponse{Value: timeValue, ContainerId: GetContainerId(), InstanceId: GetInstanceId()}
	if err := json.NewEncoder(res).Encode(timeReponse); err != nil {
		log.Panic(err)
	}
}

// Call the remote web service and return the result as a string
func GetHttpResponse(url string) string {
	response, err := http.Get(url)
	if err != nil {
		log.Fatal(err)
	}
	defer response.Body.Close()

	contents, err := ioutil.ReadAll(response.Body)
	if err != nil {
		log.Fatal(err)
	}
	return string(contents)

}

// set the HTTP BASIC AUTH username based on env variable TIME_USERNAME
// if not present return "admin"
func GetHttpUsername() string {
	if http_username := os.Getenv("TIME_USERNAME"); len(http_username) > 0 {
		return http_username
	}
	return "admin" // default username
}

// set the HTTP BASIC AUTH password based on env variable TIME_PASSWORD
// if not present return "password"
func GetHttpPassord() string {
	if http_password := os.Getenv("TIME_PASSWORD"); len(http_password) > 0 {
		return http_password
	}
	return "password" // default password
}

// Get the ContainerID if exists
func GetContainerId() string {
	cmd := "cat /proc/self/cgroup | grep \"docker\" | sed s/\\\\//\\\\n/g | tail -1"
	out, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		log.Printf("Container Id err is %s\n", err)
		return ""
	}
	log.Printf("The container id is %s\n", out)
	return strings.TrimSpace(string(out))
}

// Get the Instance ID if exists
func GetInstanceId() string {
	cmd := "curl"
	cmdArgs := []string{"-s", "http://169.254.169.254/latest/meta-data/instance-id"}
	out, err := exec.Command(cmd, cmdArgs...).Output()
	if err != nil {
		log.Printf("Instance Id err is %s\n", err)
		return ""
	}
	log.Printf("The instance id is %s\n", out)
	return string(out)
}
