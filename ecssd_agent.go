package main

// Copyright 2016-2016 Amazon.com, Inc. or its affiliates. All Rights Reserved.
// Licensed under the Apache License, Version 2.0 (the "License"). You may not use this file except in compliance with the License. A copy of the License is located at
// http://aws.amazon.com/apache2.0/
// or in the "license" file accompanying this file. This file is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific language governing permissions and limitations under the License.


import (
	"fmt"
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

const workerTimeout = 60 * time.Second

type Handler interface {
	Handle(*docker.APIEvents) error
}

type DockerRouter struct {
	handlers      map[string][]Handler
	dockerClient  *docker.Client
	listener      chan *docker.APIEvents
	workers       chan *worker
	workerTimeout time.Duration
}

func dockerEventsRouter(bufferSize int, workerPoolSize int, dockerClient *docker.Client,
	handlers map[string][]Handler) (*DockerRouter, error) {
	workers := make(chan *worker, workerPoolSize)
	for i := 0; i < workerPoolSize; i++ {
		workers <- &worker{}
	}

	dockerRouter := &DockerRouter{
		handlers:      handlers,
		dockerClient:  dockerClient,
		listener:      make(chan *docker.APIEvents, bufferSize),
		workers:       workers,
		workerTimeout: workerTimeout,
	}

	return dockerRouter, nil
}

func (e *DockerRouter) start() error {
	go e.manageEvents()
	err := e.dockerClient.AddEventListener(e.listener)
	return err
}

func (e *DockerRouter) stop() error {
	if e.listener == nil {
		return nil
	}
	err := e.dockerClient.RemoveEventListener(e.listener)
	return err
}

func (e *DockerRouter) manageEvents() {
	for {
		event := <-e.listener
		timer := time.NewTimer(e.workerTimeout)
		gotWorker := false
		for !gotWorker {
			select {
			case w := <-e.workers:
				go w.doWork(event, e)
				gotWorker = true
			case <-timer.C:
				log.Infof("Timed out waiting.")
			}
		}
	}
}

type worker struct{}

func (w *worker) doWork(event *docker.APIEvents, e *DockerRouter) {
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

type Config struct {
	EcsCluster   string
	Region       string
	HostedZoneId string
	Hostname     string
}

var config Config

func testError(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func testErrorNoFatal(err error) {
	if err != nil {
		log.Error(err)
	}
}

type TopTasks struct {
	Tasks []TaskInfo
}

type TaskInfo struct {
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
		DNSName: aws.String("servicediscovery.internal"),
	}

	zones, err := r53.ListHostedZonesByName(params)

	if err == nil {
		if len(zones.HostedZones) > 0 {
			return *zones.HostedZones[0].Id, nil
		}
	}

	return "", err
}

func createDNSRecord(serviceName string, dockerId string, port string) error {
	r53 := route53.New(session.New())
	params := &route53.ChangeResourceRecordSetsInput{
		ChangeBatch: &route53.ChangeBatch{
			Changes: []*route53.Change{
				{
					Action: aws.String(route53.ChangeActionCreate),
					ResourceRecordSet: &route53.ResourceRecordSet{
						Name: aws.String(serviceName + ".servicediscovery.internal"),
						Type: aws.String(route53.RRTypeSrv),
						ResourceRecords: []*route53.ResourceRecord{
							{
								Value: aws.String("1 1 " + port + " " + config.Hostname),
							},
						},
						SetIdentifier: aws.String(dockerId),
						TTL:           aws.Int64(0),
						Weight:        aws.Int64(1),
					},
				},
			},
			Comment: aws.String("Service Discovery Created Record"),
		},
		HostedZoneId: aws.String(config.HostedZoneId),
	}
	_, err := r53.ChangeResourceRecordSets(params)
	testErrorNoFatal(err)
	fmt.Println("Record " + serviceName + ".servicediscovery.internal created (1 1 " + port + " " + config.Hostname + ")")
	return err
}

func deleteDNSRecord(serviceName string, dockerId string) error {
	var err error
	r53 := route53.New(session.New())
	paramsList := &route53.ListResourceRecordSetsInput{
		HostedZoneId:          aws.String(config.HostedZoneId), // Required
		MaxItems:              aws.String("10"),
		StartRecordIdentifier: aws.String(dockerId),
		StartRecordName:       aws.String(serviceName + ".servicediscovery.internal"),
		StartRecordType:       aws.String(route53.RRTypeSrv),
	}
	resp, err := r53.ListResourceRecordSets(paramsList)
	testErrorNoFatal(err)
	if err != nil {
		return err
	}
	srvValue := ""
	for _, rrset := range resp.ResourceRecordSets {
		if *rrset.SetIdentifier == dockerId {
			for _, rrecords := range rrset.ResourceRecords {
				srvValue = *rrecords.Value
			}
		}
	}
	if srvValue == "" {
		log.Error("Route53 Record doesn't exist")
		return nil
	}

	params := &route53.ChangeResourceRecordSetsInput{
		ChangeBatch: &route53.ChangeBatch{
			Changes: []*route53.Change{
				{
					Action: aws.String(route53.ChangeActionDelete),
					ResourceRecordSet: &route53.ResourceRecordSet{
						Name: aws.String(serviceName + ".servicediscovery.internal"),
						Type: aws.String(route53.RRTypeSrv),
						ResourceRecords: []*route53.ResourceRecord{
							{
								Value: aws.String(srvValue),
							},
						},
						SetIdentifier: aws.String(dockerId),
						TTL:           aws.Int64(0),
						Weight:        aws.Int64(1),
					},
				},
			},
		},
		HostedZoneId: aws.String(config.HostedZoneId),
	}
	_, err = r53.ChangeResourceRecordSets(params)
	testErrorNoFatal(err)
	fmt.Println("Record " + serviceName + ".servicediscovery.internal deleted ( " + srvValue + ")")
	return err
}

var dockerClient *docker.Client

func getNetworkPortAndServiceName(container *docker.Container, includePort bool) (string, string){
	for _, env := range container.Config.Env {
		envEval := strings.Split(env, "=")
		nameEval := strings.Split(envEval[0], "_")
		if len(envEval) == 2 && len(nameEval) == 3 && nameEval[0] == "SERVICE"  && nameEval[2] == "NAME" {
			if _, err := strconv.Atoi(nameEval[1]); err == nil {
				if includePort {
					for srcPort, mapping := range container.NetworkSettings.Ports {
						if strings.HasPrefix(string(srcPort), nameEval[1]) {
							if len(mapping) > 0 {
								return mapping[0].HostPort, envEval[1]
							}
						}		
					}
				} else {
					return "", envEval[1]
				}	
			}
		}
	}
	return "", ""
}

func main() {
	var err error
	var sum time.Duration
	var zoneId string
	for {
		zoneId, err = getDNSHostedZoneId()
		if err == nil {
			break
		}
		if sum > 8 {
			testError(err)
		}
		time.Sleep(sum * time.Second)
		sum += 2
	}
	config.HostedZoneId = zoneId
	metadataClient := ec2metadata.New(session.New())
	hostname, err := metadataClient.GetMetadata("/hostname")
	config.Hostname = hostname
	testError(err)

	endpoint := "unix:///var/run/docker.sock"
	startFn := func(event *docker.APIEvents) error {
		var err error
		container, err := dockerClient.InspectContainer(event.ID)
		testError(err)
		port, service := getNetworkPortAndServiceName(container, true)
		if port != "" && service != "" {
			sum = 1
			for {
				if err = createDNSRecord(service, event.ID, port); err == nil {
					break
				}
				if sum > 8 {
					log.Error("Error creating DNS record")
					break
				}
				time.Sleep(sum * time.Second)
				sum += 2
			}
		}
		fmt.Println("Docker " + event.ID + " started")
		return nil
	}

	stopFn := func(event *docker.APIEvents) error {
		var err error
		container, err := dockerClient.InspectContainer(event.ID)
		testError(err)
		_, service := getNetworkPortAndServiceName(container, false)
		if service != "" {
			sum = 1
			for {
				if err = deleteDNSRecord(service, event.ID); err == nil {
					break
				}
				if sum > 8 {
					log.Error("Error deleting DNS record")
					break
				}
				time.Sleep(sum * time.Second)
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
	handlers := map[string][]Handler{"start": []Handler{startHandler}, "die": []Handler{stopHandler}}

	dockerClient, _ = docker.NewClient(endpoint)
	router, err := dockerEventsRouter(5, 5, dockerClient, handlers)
	testError(err)
	defer router.stop()
	router.start()
	fmt.Println("Waiting events")
	select {}
}
