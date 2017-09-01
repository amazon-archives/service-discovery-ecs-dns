package main

import (
	"encoding/json"
	"github.com/goji/httpauth"
	"github.com/gorilla/mux"
	"github.com/marcmak/calc/calc"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Spec represents the Calc response object.
type CalcResponse struct {
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
	r.HandleFunc("/calc/{formula}", CalcHandler).Methods("GET")
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
func CalcHandler(res http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	formula := vars["formula"]
	log.Printf("Calc formula is %s\n", formula)

	result := strconv.FormatFloat(calc.Solve(formula), 'G', 16, 64)

	calcReponse := CalcResponse{Value: result, ContainerId: GetContainerId(), InstanceId: GetInstanceId()}
	if err := json.NewEncoder(res).Encode(calcReponse); err != nil {
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

// set the HTTP BASIC AUTH username based on env variable CALC_USERNAME
// if not present return "admin"
func GetHttpUsername() string {
	if http_username := os.Getenv("CALC_USERNAME"); len(http_username) > 0 {
		return http_username
	}
	return "admin" // default username
}

// set the HTTP BASIC AUTH password based on env variable CALC_PASSWORD
// if not present return "password"
func GetHttpPassord() string {
	if http_password := os.Getenv("CALC_PASSWORD"); len(http_password) > 0 {
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
