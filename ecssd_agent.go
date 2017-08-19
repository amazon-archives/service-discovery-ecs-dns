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
	"github.com/aws/aws-sdk-go/service/cloudwatchevents"
	"net/http"
	"io/ioutil"
	"strconv"
	"strings"
	"time"
	"flag"
)

const workerTimeout = 180 * time.Second
const defaultTTL = 0
const defaultWeight = 1

var DNSName = "servicediscovery.internal"

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
	HostedZoneId string
	Hostname     string
	Region		 string
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

type ServiceInfo struct {
	Name string
	Port string
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

func createARecord(hostName string, localIP string) error {
	r53 := route53.New(session.New())
	aRecordName := configuration.Hostname
	// This API call creates a new DNS record for this service
	params := &route53.ChangeResourceRecordSetsInput{
		ChangeBatch: &route53.ChangeBatch{
			Changes: []*route53.Change{
				{
					Action: aws.String(route53.ChangeActionCreate),
					ResourceRecordSet: &route53.ResourceRecordSet{
						Name: aws.String(aRecordName),
						// It creates an A record with the IP of the host running the agent
						Type: aws.String(route53.RRTypeA),
						ResourceRecords: []*route53.ResourceRecord{
							{
								// priority: the priority of the target host, lower value means more preferred
								// weight: A relative weight for records with the same priority, higher value means more preferred
								// port: the TCP or UDP port on which the service is to be found
								// target: the canonical hostname of the machine providing the service
								Value: aws.String(localIP),
							},
						},
						// TTL=0 to avoid DNS caches
						TTL:    aws.Int64(defaultTTL),
					},
				},
			},
			Comment: aws.String("Host A Record Created"),
		},
		HostedZoneId: aws.String(configuration.HostedZoneId),
	}
	_, err := r53.ChangeResourceRecordSets(params)
	logErrorNoFatal(err)
	fmt.Println("Record " + configuration.Hostname + " created, resolves to  " + localIP)
	return err
}

func createDNSRecord(serviceName string, dockerId string, port string) error {
	r53 := route53.New(session.New())
	srvRecordName := serviceName + "." + DNSName
	// This API call creates a new DNS record for this service
	params := &route53.ChangeResourceRecordSetsInput{
		ChangeBatch: &route53.ChangeBatch{
			Changes: []*route53.Change{
				{
					Action: aws.String(route53.ChangeActionCreate),
					ResourceRecordSet: &route53.ResourceRecordSet{
						Name: aws.String(srvRecordName),
						// It creates a SRV record with the name of the service
						Type: aws.String(route53.RRTypeSrv),
						ResourceRecords: []*route53.ResourceRecord{
							{
								// priority: the priority of the target host, lower value means more preferred
								// weight: A relative weight for records with the same priority, higher value means more preferred
								// port: the TCP or UDP port on which the service is to be found
								// target: the canonical hostname of the machine providing the service
								Value: aws.String("1 1 " + port + " " + configuration.Hostname),
							},
						},
						SetIdentifier: aws.String(dockerId),
						// TTL=0 to avoid DNS caches
						TTL:    aws.Int64(defaultTTL),
						Weight: aws.Int64(defaultWeight),
					},
				},
			},
			Comment: aws.String("Service Discovery Created Record"),
		},
		HostedZoneId: aws.String(configuration.HostedZoneId),
	}
	_, err := r53.ChangeResourceRecordSets(params)
	logErrorNoFatal(err)
	fmt.Println("Record " + srvRecordName + " created (1 1 " + port + " " + configuration.Hostname + ")")
	return err
}

func deleteDNSRecord(serviceName string, dockerId string) error {
	var err error
	r53 := route53.New(session.New())
	srvRecordName := serviceName + "." + DNSName
	// This API Call looks for the Route53 DNS record for this service and docker ID to get the values to delete
	paramsList := &route53.ListResourceRecordSetsInput{
		HostedZoneId:          aws.String(configuration.HostedZoneId), // Required
		MaxItems:              aws.String("10"),
		StartRecordIdentifier: aws.String(dockerId),
		StartRecordName:       aws.String(srvRecordName),
		StartRecordType:       aws.String(route53.RRTypeSrv),
	}
	resp, err := r53.ListResourceRecordSets(paramsList)
	logErrorNoFatal(err)
	if err != nil {
		return err
	}
	srvValue := ""
	for _, rrset := range resp.ResourceRecordSets {
		if *rrset.SetIdentifier == dockerId && (*rrset.Name == srvRecordName || *rrset.Name == srvRecordName+".") {
			for _, rrecords := range rrset.ResourceRecords {
				srvValue = aws.StringValue(rrecords.Value)
				break
			}
		}
	}
	if srvValue == "" {
		log.Error("Route53 Record doesn't exist")
		return nil
	}

	// This API call deletes the DNS record for the service for this docker ID
	params := &route53.ChangeResourceRecordSetsInput{
		ChangeBatch: &route53.ChangeBatch{
			Changes: []*route53.Change{
				{
					Action: aws.String(route53.ChangeActionDelete),
					ResourceRecordSet: &route53.ResourceRecordSet{
						Name: aws.String(srvRecordName),
						Type: aws.String(route53.RRTypeSrv),
						ResourceRecords: []*route53.ResourceRecord{
							{
								Value: aws.String(srvValue),
							},
						},
						SetIdentifier: aws.String(dockerId),
						TTL:           aws.Int64(defaultTTL),
						Weight:        aws.Int64(defaultWeight),
					},
				},
			},
		},
		HostedZoneId: aws.String(configuration.HostedZoneId),
	}
	_, err = r53.ChangeResourceRecordSets(params)
	logErrorNoFatal(err)
	fmt.Println("Record " + srvRecordName + " deleted ( " + srvValue + ")")
	return err
}

var dockerClient *docker.Client

func getNetworkPortAndServiceName(container *docker.Container, includePort bool) []ServiceInfo {
	// One of the environment varialbles should be SERVICE_<port>_NAME = <name of the service>
	// We look for this environment variable doing a split in the "=" and another one in the "_"
	// So envEval = [SERVICE_<port>_NAME, <name>]
	// nameEval = [SERVICE, <port>, NAME]
	var svc []ServiceInfo = make([]ServiceInfo, 0)
	for _, env := range container.Config.Env {
		envEval := strings.Split(env, "=")
		nameEval := strings.Split(envEval[0], "_")
		if len(envEval) == 2 && len(nameEval) == 3 && nameEval[0] == "SERVICE" && nameEval[2] == "NAME" {
			if _, err := strconv.Atoi(nameEval[1]); err == nil {
				if includePort {
					for srcPort, mapping := range container.NetworkSettings.Ports {
						portEval := strings.Split(string(srcPort), "/")
						if len(portEval) > 0 && portEval[0] == nameEval[1] {
							if len(mapping) > 0 {
								svc = append(svc, ServiceInfo{envEval[1], mapping[0].HostPort})
							}
						}
					}
				} else {
					svc = append(svc, ServiceInfo{envEval[1], ""})
				}
			}
		}
	}
	return svc
}

func sendToCWEvents (detail string, detailType string, resource string, source string) error {
	config := aws.NewConfig().WithRegion(configuration.Region)
	sess := session.New(config)
	svc := cloudwatchevents.New(sess)
	params := &cloudwatchevents.PutEventsInput{
		Entries: []*cloudwatchevents.PutEventsRequestEntry{
			{
				Detail: aws.String(detail),
				DetailType: aws.String(detailType),
				Resources: []*string{
					aws.String(resource),
				},
				Source: aws.String(source),
				Time: aws.Time(time.Now()),
			},
		},
	}
	_, err := svc.PutEvents(params)
	logErrorNoFatal(err)
	return err
}

func getTaskArn(dockerID string) string {
	resp, err := http.Get("http://127.0.0.1:51678/v1/tasks")
	if err != nil {
		logErrorAndFail(err)
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	bodyStr := string(body)
	idIndex := strings.Index(bodyStr, string(dockerID))
	arnStartIndex := strings.LastIndex(bodyStr[:idIndex], "arn:aws:ecs:")
	arnString := bodyStr[arnStartIndex:]
	arnEndIndex := strings.Index(arnString, "\"")
	return arnString[:arnEndIndex]
}

func main() {
	var err error
	var sum int
	var zoneId string

	var sendEvents = flag.Bool("cw-send-events", false, "Send CloudWatch events when a container is created or terminated")
	
	flag.Parse()

	var DNSNameArg = flag.Arg(0)
	if DNSNameArg != "" {
		DNSName = DNSNameArg
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
	localIP, err := metadataClient.GetMetadata("/local-ipv4")
	configuration.Hostname = "IP-" + strings.Replace(localIP, ".", "-", 3) + "." + DNSName
	logErrorAndFail(err)
	region, err := metadataClient.Region()
	configuration.Region = region
	logErrorAndFail(err)

	if err = createARecord(configuration.Hostname, localIP); err != nil {
		log.Error("Error creating host A record")
	}

	endpoint := "unix:///var/run/docker.sock"
	startFn := func(event *docker.APIEvents) error {
		var err error
		container, err := dockerClient.InspectContainer(event.ID)
		logErrorAndFail(err)
		allService := getNetworkPortAndServiceName(container, true)
		for _, svc := range allService {
			if svc.Name != "" && svc.Port != "" {
				sum = 1
				for {
					if err = createDNSRecord(svc.Name, event.ID, svc.Port); err == nil {
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
		}
		if *sendEvents {
			taskArn := getTaskArn(event.ID)
			sendToCWEvents(`{ "dockerId": "` + event.ID + `","TaskArn":"` + taskArn + `" }`, "Task Started", configuration.Hostname, "awslabs.ecs.container" )
		}
		fmt.Println("Docker " + event.ID + " started")
		return nil
	}

	stopFn := func(event *docker.APIEvents) error {
		var err error
		container, err := dockerClient.InspectContainer(event.ID)
		logErrorAndFail(err)
		allService := getNetworkPortAndServiceName(container, false)
		for _, svc := range allService {
			if svc.Name != "" {
				sum = 1
				for {
					if err = deleteDNSRecord(svc.Name, event.ID); err == nil {
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
		}
		if *sendEvents {
			taskArn := getTaskArn(event.ID)
			sendToCWEvents(`{ "dockerId": "` + event.ID + `","TaskArn":"` + taskArn + `" }`, "Task Stopped", configuration.Hostname, "awslabs.ecs.container" )
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
	fmt.Println("Waiting events")
	select {}
}
