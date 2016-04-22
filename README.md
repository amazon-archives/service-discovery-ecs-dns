# Service Discovery for AWS EC2 Container Service
## Goals
This project has been created to facilitate the creation of MicroServices on top of AWS ECS.

Some of the tenets are:

* Start services in any order
* Stop services with confidence
* Automatically register/de-register services when started/stopped
* Load balance access to services
* Monitor the health of the service

## Installation
You need a private hosted zone in Route53 to register all the containers for each service. You can create the hosted zone using the CloudFormation template "privatedns.cform".

To create an ECS Cluster with all the required configuration you can use the CloudFormation template "environment.cform". This template creates an Autoscaling Configuration and Group, and an ECS cluster.

You should create a Lambda function to monitor the services, in case a host fails completely and the agent cannot delete the records. You can also use the Lambda function to do HTTP health checks for your containers.

Create a role for the Lambda function, this role should have full access to Route53 (at least to the intenal hosted zone), read only access to ECS and read only access to EC2.

Create a lambda function using the code in lambda_health_check.py, you can modify the parameters in the funtion:

* ecs_clusters: This is an array with all the clusters with the agent installed. You can leave it empty and the function will get the list of clusters from your account.
* check_health: Indicate if you want to do HTTP Health Check to all the containers.
* check_health_path: The path of the Health Check URL in the containers.

You should then schedule the Lambda funtion to run every 5 minutes.

## Usage
Once the cluster is created, you can start launching tasks and services into the ECS Cluster. For each task you want to register as a MicroService, you should specify an Environment variable in the Task definition, the name of the variable should be SERVICE_\<port>_NAME, where \<port> is the port where your service is going to listen inside the container, and the value is the name of the microservice. You can define multiple services per container using different ports.

You should publish the port of the container using the portMappings properties. When you publish the port I recommend you to not specify the containerPort and leave it to be assigned randomly, this way you could have multiple containers of the same service running in the same server.

When the service starts, and the container is launched in one of the servers, the ecssd agent registers a new DNS record automatically, with the name <serviceName>.servicediscovery.internal and the type SRV.

You can use this name to access the service from your consumers, Route53 balances the requests between your different containers for the same service. For example in go you can use:

```golang
func getServiceEnpoint() (string, error) {
	var addrs []*net.SRV
  	var err error
	if _, addrs, err = net.LookupSRV("", "", "serviceName.servicediscovery.internal"); err != nil {
		return "", err
	}
	for _, addr := range addrs {
		return strings.TrimRight(addr.Target, ".") + ":" + strconv.Itoa(int(addr.Port)), nil
	}
	return "", errors.New("No record found")
}
```

## Example

We've included an example of usage of the service discovery, the example is composed of the following containers:

* time: This container is a web service receiving a string with a time format, and returns the current time in that format. The format is a combination of the following date: "Mon Jan 2 15:04:05 -0700 MST 2006", for example: "15:04 Jan 2".
To test the service you can use: 
```
curl -u admin:password 127.0.0.1:32804/time/15:04%20Jan%202
```
* calc: This container is a web service to resolve a mathematical formula. The input is a formula and it returns the result, for example "(2+2)*3". To test the service you can use:
```
curl -u admin:password 127.0.0.1:32799/calc/\(2+2\)*3
```
* portal: This is a web service to provide a web portal with two boxes to test time and calc services. The portal uses the service discovery DNS to discover the other services and send the request to them showing the results in the web page.

You can launch the examples using the cloudformation template "samples.cform", then you can connect to the portal from a browser and test both microservices and the service discovery.

You can review the Route53 records created by the service (only for time and calc, because portal is not a microservice and it doesn't provide the SERVICE_\<port>_NAME env variable), and stop a container to see how the Route53 records change automatically.

