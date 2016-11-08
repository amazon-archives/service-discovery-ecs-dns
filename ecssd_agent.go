package main

// Copyright 2016-2016 Amazon.com, Inc. or its affiliates. All Rights Reserved.
// Licensed under the Apache License, Version 2.0 (the "License"). You may not use this file except in compliance with the License. A copy of the License is located at
// http://aws.amazon.com/apache2.0/
// or in the "license" file accompanying this file. This file is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific language governing permissions and limitations under the License.


import (
	"fmt"
	"os"
	log "github.com/Sirupsen/logrus"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/fsouza/go-dockerclient"
	"time"
	"strings"
	"strconv"
)

const workerTimeout = 180 * time.Second
const defaultTTL = 60

var DNSName = "servicediscovery.local"

type handler interface {
	Handle(*docker.APIEvents) error
}

type dockerRouter struct {
	handlers      map[string][]handler
	dockerClient  *docker.Client
	listener      chan *docker.APIEvents
	workers       chan *worker
	workerTimeout time.Duration
}

func dockerEventsRouter(bufferSize int, workerPoolSize int, dockerClient *docker.Client,
	handlers map[string][]handler) (*dockerRouter, error) {
	workers := make(chan *worker, workerPoolSize)
	for i := 0; i < workerPoolSize; i++ {
		workers <- &worker{}
	}

	dockerRouter := &dockerRouter{
		handlers:      handlers,
		dockerClient:  dockerClient,
		listener:      make(chan *docker.APIEvents, bufferSize),
		workers:       workers,
		workerTimeout: workerTimeout,
	}

	return dockerRouter, nil
}

func (e *dockerRouter) start() error {
	go e.manageEvents()
	return e.dockerClient.AddEventListener(e.listener)
}

func (e *dockerRouter) stop() error {
	if e.listener == nil {
		return nil
	}
	return e.dockerClient.RemoveEventListener(e.listener)
}

func (e *dockerRouter) manageEvents() {
	for {
		event := <-e.listener
		timer := time.NewTimer(e.workerTimeout)
		gotWorker := false
		// Wait until we get a free worker or a timeout
		// there is a limit in the number of concurrent events managed by workers to avoid resource exhaustion
		// so we wait until we have a free worker or a timeout occurs
		for !gotWorker {
			select {
			case w := <-e.workers:
				if !timer.Stop() {
					<-timer.C
				}
				go w.doWork(event, e)
				gotWorker = true
			case <-timer.C:
				log.Infof("Timed out waiting.")
			}
		}
	}
}

type worker struct{}

func (w *worker) doWork(event *docker.APIEvents, e *dockerRouter) {
	defer func() { e.workers <- w }()
	if handlers, ok := e.handlers[event.Status]; ok {
		log.Infof("Processing event: %#v", event)
		for _, handler := range handlers {
			if err := handler.Handle(event); err != nil {
				log.Errorf("Error processing event %#v. Error: %v", event, err)
			}
		}
	}
}

type dockerHandler struct {
	handlerFunc func(event *docker.APIEvents) error
}

func (th *dockerHandler) Handle(event *docker.APIEvents) error {
	return th.handlerFunc(event)
}

type config struct {
	EcsCluster   string
	Region       string
	HostedZoneId string
	LocalIp      string
}

var configuration config

func logErrorAndFail(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func logErrorNoFatal(err error) {
	if err != nil {
		log.Error(err)
	}
}

type topTasks struct {
	Tasks []taskInfo
}

type taskInfo struct {
	Arn           string
	DesiredStatus string
	KnownStatus   string
	Family        string
	Version       string
	Containers    []ContainerInfo
}

type ContainerInfo struct {
	DockerId   string
	DockerName string
	Name       string
}

func getDNSHostedZoneId() (string, error) {
	r53 := route53.New(session.New())
	params := &route53.ListHostedZonesByNameInput{
		DNSName: aws.String(DNSName),
	}

	zones, err := r53.ListHostedZonesByName(params)

	if err == nil {
		if len(zones.HostedZones) > 0 {
			return aws.StringValue(zones.HostedZones[0].Id), nil
		}
	}

	return "", err
}

func createDNSRecord(serviceName string) error {
	r53 := route53.New(session.New())
	// This API Call looks for the Route53 DNS record for this service to update
	paramsList := &route53.ListResourceRecordSetsInput{
		HostedZoneId:          aws.String(configuration.HostedZoneId), // Required
		MaxItems:              aws.String("10"),
		StartRecordName:       aws.String(serviceName + "." + DNSName),
		StartRecordType:       aws.String(route53.RRTypeA),
	}
	resp, err := r53.ListResourceRecordSets(paramsList)
	logErrorNoFatal(err)
	if err != nil {
		return err
	}
	// Merge the A records.
	aValues := resp.ResourceRecordSets[0].ResourceRecords
	if aValues == nil {
		aValues = make([]*route53.ResourceRecord, 1)
	}
	// the private IPv4 address of the machine providing the service
	rr := route53.ResourceRecord{Value: aws.String(configuration.LocalIp)}
	aValues = append(aValues, &rr)

	// This API call creates a new DNS record for this service
	params := &route53.ChangeResourceRecordSetsInput{
		ChangeBatch: &route53.ChangeBatch{
			Changes: []*route53.Change{
				{
					Action: aws.String(route53.ChangeActionUpsert),
					ResourceRecordSet: &route53.ResourceRecordSet{
						Name: aws.String(serviceName + "." + DNSName),
						// It creates an A record with the name of the service
						Type: aws.String(route53.RRTypeA),
						ResourceRecords: aValues,
						TTL: aws.Int64(defaultTTL),
					},
				},
			},
			Comment: aws.String("Service Discovery Created Record"),
		},
		HostedZoneId: aws.String(configuration.HostedZoneId),
	}
	_, err = r53.ChangeResourceRecordSets(params)
	logErrorNoFatal(err)
		fmt.Println("Record " + serviceName + "." + DNSName + " added (" + configuration.LocalIp + ")")
	return err
}

func deleteDNSRecord(serviceName string) error {
	var err error
	r53 := route53.New(session.New())
	// This API Call looks for the Route53 DNS record for this service and docker ID to get the values to delete
	paramsList := &route53.ListResourceRecordSetsInput{
		HostedZoneId:          aws.String(configuration.HostedZoneId), // Required
		MaxItems:              aws.String("10"),
		StartRecordName:       aws.String(serviceName + "." + DNSName),
		StartRecordType:       aws.String(route53.RRTypeA),
	}
	resp, err := r53.ListResourceRecordSets(paramsList)
	logErrorNoFatal(err)
	if err != nil {
		return err
	}
	// Purge the A records of the local IP
	aValues := resp.ResourceRecordSets[0].ResourceRecords
	if aValues == nil {
		log.Error("Route53 Record doesn't exist")
		return nil
	}

	changedRecord := false

	for i, aValue := range aValues {
		if *aValue.Value == configuration.LocalIp {
			// delete this ResourceRecord
			aValues = append(aValues[:i], aValues[i+1])
			changedRecord = true
			break
		}
	}

	if changedRecord == false {
		return err
	}

	// This API call deletes the DNS record for the service for this docker ID
	params := &route53.ChangeResourceRecordSetsInput{
		ChangeBatch: &route53.ChangeBatch{
			Changes: []*route53.Change{
				{
					Action: aws.String(route53.ChangeActionDelete),
					ResourceRecordSet: &route53.ResourceRecordSet{
						Name: aws.String(serviceName + "." + DNSName),
						Type: aws.String(route53.RRTypeA),
						ResourceRecords: aValues,
						TTL: aws.Int64(defaultTTL),
					},
				},
			},
		},
		HostedZoneId: aws.String(configuration.HostedZoneId),
	}
	_, err = r53.ChangeResourceRecordSets(params)
	logErrorNoFatal(err)
	fmt.Println("Record " + serviceName + "." + DNSName + " deleted ( " + configuration.LocalIp + ")")
	return err
}

var dockerClient *docker.Client

func getNetworkPortAndServiceName(container *docker.Container) (string){
	// One of the environment varialbles should be SERVICE_<port>_NAME = <name of the service>
	// We look for this environment variable doing a split in the "=" and another one in the "_"
	// So envEval = [SERVICE_<port>_NAME, <name>]
	// nameEval = [SERVICE, <port>, NAME]
	for _, env := range container.Config.Env {
		envEval := strings.Split(env, "=")
		nameEval := strings.Split(envEval[0], "_")
		if len(envEval) == 2 && len(nameEval) == 3 && nameEval[0] == "SERVICE"  && nameEval[2] == "NAME" {
			if _, err := strconv.Atoi(nameEval[1]); err == nil {
				return envEval[1]
			}
		}
	}
	return ""
}

func main() {
	var err error
	var sum int
	var zoneId string
	if len(os.Args) > 1 {
		DNSName = os.Args[1]
	}
	for {
		// We try to get the Hosted Zone Id using exponential backoff
		zoneId, err = getDNSHostedZoneId()
		if err == nil {
			break
		}
		if sum > 8 {
			logErrorAndFail(err)
		}
		time.Sleep(time.Duration(sum) * time.Second)
		sum += 2
	}
	configuration.HostedZoneId = zoneId
	metadataClient := ec2metadata.New(session.New())
	localIp, err := metadataClient.GetMetadata("/local-ipv4")
	logErrorAndFail(err)
	configuration.LocalIp = localIp

	endpoint := "unix:///var/run/docker.sock"
	startFn := func(event *docker.APIEvents) error {
		var err error
		container, err := dockerClient.InspectContainer(event.ID)
		logErrorAndFail(err)
		service := getNetworkPortAndServiceName(container)
		if service != "" {
			sum = 1
			for {
				if err = createDNSRecord(service); err == nil {
					break
				}
				if sum > 8 {
					log.Error("Error creating DNS record")
					break
				}
				time.Sleep(time.Duration(sum) * time.Second)
				sum += 2
			}
		}
		fmt.Println("Docker " + event.ID + " started")
		return nil
	}

	stopFn := func(event *docker.APIEvents) error {
		var err error
		container, err := dockerClient.InspectContainer(event.ID)
		logErrorAndFail(err)
		service := getNetworkPortAndServiceName(container)
		if service != "" {
			sum = 1
			for {
				if err = deleteDNSRecord(service); err == nil {
					break
				}
				if sum > 8 {
					log.Error("Error deleting DNS record")
					break
				}
				time.Sleep(time.Duration(sum) * time.Second)
				sum += 2
			}
		}
		fmt.Println("Docker " + event.ID + " stopped")
		return nil
	}

	startHandler := &dockerHandler{
		handlerFunc: startFn,
	}
	stopHandler := &dockerHandler{
		handlerFunc: stopFn,
	}

	handlers := map[string][]handler{"start": []handler{startHandler}, "die": []handler{stopHandler}}

	dockerClient, _ = docker.NewClient(endpoint)
	router, err := dockerEventsRouter(5, 5, dockerClient, handlers)
	logErrorAndFail(err)
	defer router.stop()
	router.start()
	fmt.Println("Waiting for events")
	select {}
}
