# Service Discovery for AWS EC2 Container Service
## Goals
This project has been created to facilitate the creation of microservices on top of AWS ECS.

Some of the tenets are:

* Start services in any order
* Stop services with confidence
* Automatically register/de-register services when started/stopped
* Load balance access to services
* Monitor the health of the service

## Installation
You need a private hosted zone in Route53 to register all the containers for each service. 

To create an ECS cluster with all the required configuration and the Route53 domain and the example microservices you can use the [CloudFormation template](Service_Discovery_Using_DNS.template).

You should create a Lambda function to monitor the services, in case a host fails completely and the agent cannot delete the records. You can also use the Lambda function to do HTTP health checks for your containers.

Create a role for the Lambda function, this role should have full access to Route53 (at least to the internal hosted zone), read only access to ECS, read only access to EC2 and to your VPC. The Lambda function needs to call the AWS APIs and should be added to a subnet that provides internet access via a NAT gateway and should have a security group that allows the respective outbound traffic.

Example CloudFormation template:
```
    "LambdaRole": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "AssumeRolePolicyDocument": {
          "Version" : "2012-10-17",
          "Statement": [ {
            "Effect": "Allow",
            "Principal": {
              "Service": [ "lambda.amazonaws.com" ]
            },
            "Action": [ "sts:AssumeRole" ]
          } ]
        },
        "ManagedPolicyArns": [
          "arn:aws:iam::aws:policy/service-role/AWSLambdaVPCAccessExecutionRole",
          "arn:aws:iam::aws:policy/AmazonRoute53FullAccess",
          "arn:aws:iam::aws:policy/AmazonEC2ReadOnlyAccess"
        ],
        "Policies": [
          {
            "PolicyName": "ecs-read-only",
            "PolicyDocument": {
              "Statement": [
                {
                  "Effect": "Allow",
                  "Action": [
                    "ecs:Describe*",
                    "ecs:List*"
                  ],
                  "Resource": "*"
                }
              ]
            }
          }
        ],
        "Path": "/"
      }
    }
```

Create a Lambda function using [this code](lambda_health_check.py), you can modify the parameters in the funtion:

* `ecs_clusters`: This is an array with all the clusters with the agent installed. You can leave it empty and the function will get the list of clusters from your account.
* `check_health`: Indicate if you want to do HTTP Health Check to all the containers.
* `check_health_path`: The path of the Health Check URL in the containers.

You should then schedule the Lambda funtion to run every 5 minutes with the `python3.6` runtime.

## Usage
Once the cluster is created, you can start launching tasks and services into the ECS Cluster. For each task you want to register as a microservice, you should specify an environment variable in the task definition, the name of the variable should be `SERVICE_<port>_NAME`, where `<port>` is the port where your service is going to listen inside the container, and the value is the name of the microservice with the standard scheme `_service._proto` (see https://en.wikipedia.org/wiki/SRV_record), for example `SERVICE_8081_NAME=_calc._tcp`. You can define multiple services per container using different ports.

You should publish the port of the container using the `PortMappings` properties. When you publish the port I recommend you to not specify the `HostPort` and leave it to be assigned randomly within the ephemeral port range, this way you could have multiple containers of the same service running in the same server.

When the service starts, and the container is launched in one of the servers, the ecssd agent registers a new DNS record automatically, with the name `<serviceName>.servicediscovery.internal` and the type `SRV`. For each instance, the agent also creates a new A record to make sure that the host name resolves in the private hosted zone.

You can use this name to access the service from your consumers, Route53 balances the requests between your different containers for the same service. For example in Go you can use:

```golang
func getServiceEndpoint() (string, error) {
	var addrs []*net.SRV
  	var err error
	if _, addrs, err = net.LookupSRV("serviceName", "tcp", "servicediscovery.internal"); err != nil {
		return "", err
	}
	for _, addr := range addrs {
		return addr.Target + ":" + strconv.Itoa(int(addr.Port)), nil
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

You can launch the examples using the [CloudFormation template](Service_Discovery_Using_DNS.template), then you can connect to the portal from a browser and test both microservices and the service discovery.

You can review the Route53 records created by the service (only for time and calc, because portal is not a microservice and it doesn't provide the `SERVICE_<port>_NAME` environment variable), and stop a container to see how the Route53 records change automatically.
