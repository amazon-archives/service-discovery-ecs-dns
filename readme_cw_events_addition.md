ECS Service Discovery with CloudWatch Events Notifications

Goal:
	The goal of this project is to provide a way to get information about services, including their dockerID’s and their Task ARN's
	as they start and stop on an ECS cluster.

Implementation Details: 

	This project is an extension to the service discovery for AWS EC2 Container service, found here: 
	https://github.com/awslabs/service-discovery-ecs-dns. 

	The CloudFormation template to run this solution can be found here: 
	https://s3-us-west-2.amazonaws.com/yash-bucket/ecs-service-discover-cwlogging.template

	It expands on the ECS service discovery agent by adding code that collects dockerId’s and their associated Task Arn's 
	for services starting and stopping on an instance and pushing this information to CloudWatch events. Once this information
	is sent to CloudWatch events, a lambda is triggered. There are two CloudWatch events rules that make this possible. The first
	rule checks to see if any information being pushed to CloudWatch Events is about services starting, and triggers the lambda
	function if it is. The second rule checks if information pushed to CloudWatch Events is regarding services stopping, and triggers
	the same lambda function. The lambda function simply prints the information in the events parameter in its handler to a log file. 

	Below is an example of the information in the events parameter of the lambda function:

			{
				"account": "016893804762",
				"region": "us-west-2",
				"detail": {
					"TaskArn": "arn:aws:ecs:us-west-2:016893804762:task/e90d0978-fe1d-4ea6-bb64-ce7935de6f83",
					"dockerId": "6d3bbdfcb31c4a06f3d7d6dd43403517ec1d7a91987b9a63ec3c425403b0fe5c"
				},
				"detail-type": "Task Started",
				"source": "ip-10-5-10-74.us-west-2.compute.internal",
				"version": "0",
				"time": "2016-09-09T05:21:00Z",
				"id": "9878626f-a8b3-4176-8dae-247b88719df0",
				"resources": [ "ip-10-5-10-74.us-west-2.compute.internal" ]
			}

	Notice that the this contains the TaskArn, dockerId, and if a service is starting or stopping, along with other 
	information related to the CloudWatch event. This information can be used to take subsequent actions from the lambda function. 

	This information can be viewed under CloudWatch Logs in a log group called "/aws/lambda/TaskToDockerIdMapper"

	Apart from this, the solution also installs the CloudWatch Logs agent on EC2 instances in the ECS cluster. This agent 
	watches the log file produced by the service discovery agent (which pushes information to CloudWatch Events), and pushes
	this information to CloudWatch Logs. The log can be found under a log group called "/var/log/ecssd_agent.log" in CloudWatch logs. 

	The CloudWatch agent is by default configured to work in the us-west-2 region only. To change this, you need to change the region 
	in the awscli.conf file.

Files changed from ECS service discovery github:

	1. ecssd_agentv2.go - This is the code for the ECS service discover agent. This was modified to collect ECS Task Arn's by making a
	call to the ECS Agent Introspection API and parsing the response. It also sends the TaskArn, the dockerId, and if a service is
	starting or stopping to CloudWatch logs whenever services start and stop. 
	https://s3-us-west-2.amazonaws.com/yash-bucket/ecssd_agentv2.go

	2. ecssdv2 - this is the executable file compiled from the code and is started whenever an EC2 instance is launched as part of the 
	ECS cluster though user data in the launch configuration.
	https://s3-us-west-2.amazonaws.com/yash-bucket/ecssdv2

	3. awslogs.conf - This is a configuration file for the CloudWatch Logs agent which sets up the agent to listen to the log file 
	produced though the service discovery agent and sends this log information to CloudWatch Logs. 
	https://s3-us-west-2.amazonaws.com/yash-bucket/awslogs.conf

	4. awscli.conf - This configuration file for the CloudWatch Logs agent allows you to specify which region the agent is running in.
	https://s3-us-west-2.amazonaws.com/yash-bucket/awscli.conf

Other additions: 
	- IAM policies and roles created to use Lambda and CloudWatch events
	- Added CloudWatch Events rules
	- Added lambda function
	- Launch configuration of auto scaling group updated with user data and cfn-init to download necessary file and run the agents