# Mattermost Cloud Database Factory Vertical Scaling

This repository houses the open-source components of Mattermost Cloud Database Factory Vertical Scaling. This is a microservice with the purpose of checking CPU and Memory performance of Aurora RDS Cluster instances and scale vertically by increasing the instance class.

## Microservice Dependencies

For this tool to work a couple of infrastructure components are required.
A cloudwatch alarm is deployed (by [Mattermost Cloud Database Factory](https://github.com/mattermost/mattermost-cloud-database-factory)) for each of the RDS cluster nodes that gets in alarm state when the CPU or Memory is over a preconfigured value. This alarm sends a notification to a SNS topic, which then adds a message to a SQS queue. This message is picked up by the vertical scaling tool to handle instance class changes.

## Developing

### Environment Setup
1. Install [Go](https://golang.org/doc/install)
2. Specify the region in your AWS config, e.g. `~/.aws/config`:
```
[profile mm-cloud]
region = us-east-1
```
3. Generate an AWS Access and Secret key pair, then export them in your bash profile:
  ```
  export AWS_ACCESS_KEY_ID=YOURACCESSKEYID
  export AWS_SECRET_ACCESS_KEY=YOURSECRETACCESSKEY
  export AWS_PROFILE=mm-cloud
  ```
4. Clone this repository into your GOPATH (or anywhere if you have Go Modules enabled)
5. Export the following environment variables:
  ```
  export QueueURL="The URL of the SQS queue that receives the Cloudwatch alarm notifications"
  export MattermostNotificationsHook="The mattermost hook to use for notifications"
  export MattermostAlertsHook="The mattermost hook to use for alerts"
  ```

### Building

Simply run the following:

```
$ make build
```

### Running

Run the app with:

```
$ /go/bin/database-factory-vertical-scaling
```
