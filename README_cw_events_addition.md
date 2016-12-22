#ECS Service Discovery with CloudWatch Events Notifications

##Goal:
The goal of this project is to provide a way to get information about services, including their dockerID’s and their Task ARN's as they start and stop on an ECS cluster.

##Implementation Details: 

The CloudFormation template to run this solution can be found in the file Service_Discovery_Using_DNS_cwlogging.template

It expands on the ECS service discovery agent by adding code that collects dockerId’s and their associated Task Arn's for services starting and stopping on an instance and pushing this information to CloudWatch events. Once this information is sent to CloudWatch events, a lambda is triggered. There are two CloudWatch events rules that make this possible. The first rule checks to see if any information being pushed to CloudWatch Events is about services starting, and triggers the lambda function if it is. The second rule checks if information pushed to CloudWatch Events is regarding services stopping, and triggers the same lambda function. The lambda function simply prints the information in the events parameter in its handler to a log file. 

Below is an example of the information in the events parameter of the lambda function:

```json
{
	"account": "016893804762",
	"region": "us-west-2",
	"detail": {
		"TaskArn": "arn:aws:ecs:us-west-2:016893804762:task/e90d0978-fe1d-4ea6-bb64-ce7935de6f83",
		"dockerId": "6d3bbdfcb31c4a06f3d7d6dd43403517ec1d7a91987b9a63ec3c425403b0fe5c"
	},
	"detail-type": "Task Started",
	"source": "awslabs.ecs.container",
	"version": "0",
	"time": "2016-09-09T05:21:00Z",
	"id": "9878626f-a8b3-4176-8dae-247b88719df0",
	"resources": [ "ip-10-5-10-74.us-west-2.compute.internal" ]
}
```

Notice that the this contains the TaskArn, dockerId, and if a service is starting or stopping, along with other information related to the CloudWatch event. This information can be used to take subsequent actions from the lambda function. 

This information can be viewed under CloudWatch Logs in a log group called "/aws/lambda/TaskToDockerIdMapper"
