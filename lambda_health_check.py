from __future__ import print_function

# Copyright 2016-2016 Amazon.com, Inc. or its affiliates. All Rights Reserved.
# Licensed under the Apache License, Version 2.0 (the "License"). You may not use this file except in compliance with the License. A copy of the License is located at
# http://aws.amazon.com/apache2.0/
# or in the "license" file accompanying this file. This file is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific language governing permissions and limitations under the License.

import json
import boto3
import httplib
import re
import socket


###### Configuration

ecs_clusters = []
check_health = True
check_health_path = '/health'

####################

print('Loading function')

route53 = boto3.client('route53')
ecs = boto3.client('ecs')
ec2 = boto3.client('ec2')
response = route53.list_hosted_zones_by_name(DNSName='servicediscovery.internal')
if len(response['HostedZones']) == 0:
    raise Exception('Zone not found')
hostedZoneId = response['HostedZones'][0]['Id']

def get_ip_port(rr):
    try:
        ip = socket.gethostbyname(rr['Value'].split(' ')[3])
        return [ip, rr['Value'].split(' ')[2]]
    except:
        return [None, None] 
    
def check_health(ip, port):
    try:
        conn = httplib.HTTPConnection(ip, int(port), timeout=2)
        conn.request("GET", check_health_path)
        r1 = conn.getresponse()
        if r1.status != 200:
            return "ERROR"
    except:
        return "ERROR"

def search_ecs_instance(ip, list_ec2_instances):
    for ec2Instance in list_ec2_instances:
        if list_ec2_instances[ec2Instance]['privateIP'] == ip:
            return list_ec2_instances[ec2Instance]['instanceArn']
    
def search_task(port, ec2Instance, service, list_tasks):
    for task in list_tasks:
        if list_tasks[task]['instance'] == ec2Instance and {'service': service, 'port': port} in list_tasks[task]['containers']:
            return task
        
def search_ecs_task(ip, port, service, ecs_data):
    ec2Instance = search_ecs_instance(ip, ecs_data['ec2Instances'])
    if ec2Instance != None:
        task = search_task(port, ec2Instance, service, ecs_data['tasks'])
        if task != None:
            return task
    
def delete_route53_record(record):
    route53.change_resource_record_sets(
        HostedZoneId=hostedZoneId,
        ChangeBatch={
            'Comment': 'Service Discovery Health Check failed',
            'Changes': [
                {
                    'Action': 'DELETE',
                    'ResourceRecordSet': record
                }
            ]
        })
        
def process_records(response, ecs_data):
    for record in response['ResourceRecordSets']:
        if record['Type'] == 'SRV':
            for rr in record['ResourceRecords']:
                [ip, port] = get_ip_port(rr)
                if ip != None:
                    task=search_ecs_task(ip, port, record['Name'].split('.')[0], ecs_data)
                    if task == None:
                        delete_route53_record(record)
                        print("Record %s deleted" % rr)
                        break
                        
                    if check_health:
                        result = "Initial"
                        retries = 3
                        while retries > 0 and result != None:
                            result = check_health(ip, port)
                            retries -= 1
                        if result != None:
                            delete_route53_record(record)
                            print("Record %s deleted" % rr)
                            if task != None:
                                ecs.stop_task(
                                    cluster=ecs_data['instanceArns'][ecs_data['tasks'][task]['instance']]['cluster'],
                                    task=task,
                                    reason='Service Discovery Health Check failed'
                                )
                                print("Task %s stopped" % task)
                    
    
    if response['IsTruncated']:
        if 'NextRecordIdentifier' in response.keys():
            new_response = route53.list_resource_record_sets(
                HostedZoneId=hostedZoneId,
                StartRecordName=response['NextRecordName'],
                StartRecordType=response['NextRecordType'],
                StartRecordIdentifier=response['NextRecordIdentifier'])
        else:
            new_response = route53.list_resource_record_sets(
                HostedZoneId=hostedZoneId,
                StartRecordName=response['NextRecordName'],
                StartRecordType=response['NextRecordType'])
        process_records(new_response)
    
def search_container_port(task, port):
    for container in task['containers']:
        for network in container['networkBindings']:
            if str(network['containerPort']) == port:
                return str(network['hostPort'])

    return "0"

def get_ecs_data():
    for cluster_name in ecs_clusters:
        response = ecs.list_container_instances(cluster=cluster_name)
        list_instance_arns = {}
        for instance_arn in response['containerInstanceArns']:
            list_instance_arns[instance_arn] = {'cluster': cluster_name}
        if len(list_instance_arns.keys()) > 0:
            response = ecs.describe_container_instances(
                cluster=cluster_name,
                containerInstances=list_instance_arns.keys())
            list_ec2_instances = {}
            for instance in response['containerInstances']:
                list_ec2_instances[instance['ec2InstanceId']] = {'instanceArn': instance['containerInstanceArn']}
                list_instance_arns[instance['containerInstanceArn']]['instanceId'] = instance['ec2InstanceId']
            if len(list_ec2_instances.keys()) > 0:
                response = ec2.describe_instances(InstanceIds=list_ec2_instances.keys())
                for reservation in response['Reservations']:
                    for instance in reservation['Instances']:
                        list_ec2_instances[instance['InstanceId']]['privateIP'] = instance['PrivateIpAddress']
        
        response = ecs.list_tasks(cluster=cluster_name, desiredStatus='RUNNING')
        list_tasks = {}
        if len(response['taskArns']) > 0:
            responseTasks = ecs.describe_tasks(cluster = cluster_name, tasks = response['taskArns'])
            for task in responseTasks['tasks']:
                list_tasks[task['taskArn']] = {'instance': task['containerInstanceArn'], 'containers': []}
                responseDefinition = ecs.describe_task_definition(taskDefinition=task['taskDefinitionArn'])
                for container in responseDefinition['taskDefinition']['containerDefinitions']:
                    for env in container['environment']:
                        name_eval = env['name'].split("_")
                        if len(name_eval) == 3 and name_eval[0] == "SERVICE"  and name_eval[2] == "NAME":
                            list_tasks[task['taskArn']]['containers'].append({'service': env['value'], 'port': search_container_port(task,name_eval[1])})
                    
        return {'instanceArns': list_instance_arns, 'ec2Instances': list_ec2_instances, 'tasks': list_tasks}
        
def lambda_handler(event, context):
    #print('Starting')
    
    if len(ecs_clusters) == 0:
        response = ecs.list_clusters()
        for cluster in response['clusterArns']:
            ecs_clusters.append(cluster)

    #print (ecs_clusters)
    response = route53.list_resource_record_sets(HostedZoneId=hostedZoneId)
    
    ecs_data = get_ecs_data()
    #print(ecs_data)
    
    process_records(response, ecs_data)

    return 'Service Discovery Health Check finished'
