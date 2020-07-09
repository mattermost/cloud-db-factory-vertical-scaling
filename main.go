package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/rds"
	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

// DBInstanceClasses is used to store the available DB Instance Classes. The classes are specified with size order.
var DBInstanceClasses = []string{
	"db.t3.small",
	"db.r5.large",
	"db.r5.xlarge",
	"db.r5.2xlarge",
	"db.r5.4xlarge",
	"db.r5.8xlarge",
	"db.r5.12xlarge",
	"db.r5.16xlarge",
	"db.r5.24xlarge",
}

// SQSMessageBody is used to decode the SQS Message Body
type SQSMessageBody struct {
	Type             string `json:"type"`
	MessageID        string `json:"messageId"`
	TopicArn         string `json:"topicArn"`
	Subject          string `json:"subject"`
	Message          string `json:"message"`
	Timestamp        string `json:"timestamp"`
	SignatureVersion string `json:"signatureVersion"`
	Signature        string `json:"signature"`
	SigningCertURL   string `json:"signingCertURL"`
	UnsubscribeURL   string `json:"unsubscribeURL"`
}

// Message is used to decode the SQS Message
type Message struct {
	AlarmName        string  `json:"alarmName"`
	AlarmDescription string  `json:"alarmDescription"`
	AWSAccountID     string  `json:"awsAccountId"`
	NewStateValue    string  `json:"newStateValue"`
	NewStateReason   string  `json:"newStateReason"`
	StateChangeTime  string  `json:"stateChangeTime"`
	Region           string  `json:"region"`
	AlarmArn         string  `json:"alarmArn"`
	OldStateValue    string  `json:"oldStateValue"`
	Trigger          Trigger `json:"trigger"`
}

// Trigger is used to decode the Trigger component of the SQS Message
type Trigger struct {
	MetricName                       string       `json:"metricName"`
	Namespace                        string       `json:"namespace"`
	StatisticType                    string       `json:"statisticType"`
	Statistic                        string       `json:"statistic"`
	Unit                             string       `json:"unit"`
	Dimensions                       []Dimensions `json:"dimensions"`
	Period                           int          `json:"period"`
	EvaluationPeriods                int          `json:"evaluationPeriods"`
	ComparisonOperator               string       `json:"comparisonOperator"`
	Threshold                        float32      `json:"threshold"`
	TreatMissingData                 string       `json:"treatMissingData"`
	EvaluateLowSampleCountPercentile string       `json:"evaluateLowSampleCountPercentile"`
}

// Dimensions is used to decode the Dimensions component of the Trigger
type Dimensions struct {
	Value string `json:"value"`
	Name  string `json:"name"`
}

// DBInstance is used to store information about each DB Instance
type DBInstance struct {
	SizeIndex            int    `json:"sizeIndex"`
	DBInstanceClass      string `json:"dbInstanceType"`
	DBInstanceStatus     string `json:"dbInstanceStatus"`
	DBInstanceIdentifier string `json:"dbInstanceIdentifier"`
	DBClusterIdentifier  string `json:"dbClusterIdentifier"`
	IsClusterWriter      bool   `json:"isClusterWriter"`
}

func main() {
	err := verticalScaling()
	if err != nil {
		log.WithError(err).Error("Failed to run database factory vertical scaling")
		err = sendMattermostErrorNotification(err, "Τhe Database Factory vertical scaling failed")
		if err != nil {
			log.WithError(err).Error("Failed to send Mattermost error notification")
		}
	}
}

func verticalScaling() error {
	SQSClient, RDSClient, err := getAWSClients()
	if err != nil {
		return errors.Wrap(err, "Failed to initiate AWS Clients")
	}

	message, err := getSQSMessage(SQSClient)
	if err != nil {
		return errors.Wrap(err, "Failed to receive SQS message")
	}

	if len(message.Messages) == 0 {
		log.Info("No new messages to process, skipping...")
		return nil
	}

	sqsMessage, err := decodeSQSMessage(message)
	if err != nil {
		return errors.Wrap(err, "Failed to decode SQS message")
	}

	var dbInstance DBInstance
	dbInstance.DBInstanceIdentifier = sqsMessage.Trigger.Dimensions[0].Value

	log.Infof("Vertical scaling of multitenant database (%s) is needed. Getting database information", dbInstance.DBInstanceIdentifier)
	err = dbInstance.getDatabaseInfo(RDSClient)
	if err != nil {
		return errors.Wrapf(err, "Failed to obtain DB instance (%s) information", dbInstance.DBInstanceIdentifier)
	}

	if dbInstance.getSetDBInstanceClass() {
		log.Infof("Current DB instance class (%s)", DBInstanceClasses[dbInstance.SizeIndex])
	} else {
		return errors.Wrap(err, "Existing DB instance class not in the supported list")
	}

	newClass, err := dbInstance.getNewClassType()
	if err != nil {
		return errors.Wrapf(err, "Failed to get DB instance (%s) new class type", dbInstance.DBInstanceIdentifier)
	}

	if !dbInstance.IsClusterWriter {
		log.Infof("DB instance (%s) is a reader with instance class (%s). Calling class upgrade", dbInstance.DBInstanceIdentifier, dbInstance.DBInstanceClass)

		err := dbInstance.changeDatabaseClass(RDSClient, newClass)
		if err != nil {
			return errors.Wrapf(err, "Failed to change DB Instance (%s) class", dbInstance.DBInstanceIdentifier)
		}
	} else {
		log.Infof("DB instance (%s) is a writer with instance class (%s). Getting first available reader", dbInstance.DBInstanceIdentifier, dbInstance.DBInstanceClass)
		var dbInstanceReader DBInstance
		clusterMembers, err := dbInstance.getDBClusterMembers(RDSClient)
		if err != nil {
			return errors.Wrap(err, "Failed to get DB cluster members")
		}
		for _, member := range clusterMembers {
			if strings.Contains(*member.DBInstanceIdentifier, os.Getenv("RDSMultitenantDBInstanceNamePrefix")) {
				if *member.DBInstanceIdentifier != dbInstance.DBInstanceIdentifier {
					dbInstanceReader.DBInstanceIdentifier = *member.DBInstanceIdentifier
				}
			}
		}
		log.Infof("DB instance (%s) was selected for vertical scaling. Getting database information", dbInstanceReader.DBInstanceIdentifier)
		err = dbInstanceReader.getDatabaseInfo(RDSClient)
		if err != nil {
			return errors.Wrapf(err, "Failed to obtain DB instance (%s) information", dbInstanceReader.DBInstanceIdentifier)
		}

		if dbInstanceReader.getSetDBInstanceClass() {
			log.Infof("Current DB instance class (%s)", DBInstanceClasses[dbInstanceReader.SizeIndex])
		} else {
			return errors.Wrap(err, "Existing DB instance class not in the supported list")
		}

		if (dbInstance.SizeIndex + 1) > dbInstanceReader.SizeIndex {
			err = dbInstanceReader.changeDatabaseClass(RDSClient, newClass)
			if err != nil {
				return errors.Wrapf(err, "Failed to change DB instance (%s) class", dbInstanceReader.DBInstanceIdentifier)
			}
		}

		log.Infof("Initiating DB instance (%s) failover", dbInstanceReader.DBInstanceIdentifier)
		err = dbInstanceReader.databaseFailover(RDSClient)
		if err != nil {
			return errors.Wrapf(err, "Failed to failover DB instance (%s)", dbInstanceReader.DBInstanceIdentifier)
		}
	}
	log.Info("Vertical scaling was successfully handled, deleting SQS message")

	err = deleteSQSMessage(SQSClient, message)
	if err != nil {
		return errors.Wrap(err, "failed tο delete SQS message")
	}

	err = dbInstance.sendMattermostNotification(newClass, "Vertical scaling was succesfully handled")
	if err != nil {
		log.WithError(err).Error("failed tο send Mattermost notification")
	}
	return nil
}

func getAWSClients() (*sqs.SQS, *rds.RDS, error) {
	sess, err := session.NewSession(&aws.Config{})
	if err != nil {
		return nil, nil, errors.Wrap(err, "unable to initiate AWS session")
	}
	return sqs.New(sess), rds.New(sess), nil
}

func getSQSMessage(client *sqs.SQS) (*sqs.ReceiveMessageOutput, error) {
	queueURL := os.Getenv("QueueURL")
	message, err := client.ReceiveMessage(&sqs.ReceiveMessageInput{QueueUrl: &queueURL})
	if err != nil {
		return nil, errors.Wrap(err, "unable to get SQS message")
	}
	return message, nil
}

func decodeSQSMessage(message *sqs.ReceiveMessageOutput) (Message, error) {
	var sqsMessageBody SQSMessageBody
	var sqsMessage Message
	err := json.NewDecoder(strings.NewReader(*message.Messages[0].Body)).Decode(&sqsMessageBody)
	if err != nil {
		return sqsMessage, errors.Wrap(err, "unable to decode SQS message body")
	}

	err = json.NewDecoder(strings.NewReader(sqsMessageBody.Message)).Decode(&sqsMessage)
	if err != nil {
		return sqsMessage, errors.Wrap(err, "unable to decode SQS message")
	}
	return sqsMessage, nil
}

func deleteSQSMessage(client *sqs.SQS, message *sqs.ReceiveMessageOutput) error {
	queueURL := os.Getenv("QueueURL")
	_, err := client.DeleteMessage(&sqs.DeleteMessageInput{
		QueueUrl:      &queueURL,
		ReceiptHandle: message.Messages[0].ReceiptHandle,
	})
	if err != nil {
		return errors.Wrap(err, "unable to delete SQS message")
	}
	return nil
}

func (d *DBInstance) getDBClusterMembers(client *rds.RDS) ([]*rds.DBClusterMember, error) {
	databaseClusters, err := client.DescribeDBClusters(&rds.DescribeDBClustersInput{DBClusterIdentifier: &d.DBClusterIdentifier})
	if err != nil {
		return nil, errors.Wrap(err, "unable to describe DB Cluster")
	}

	if len(databaseClusters.DBClusters) == 0 {
		return nil, errors.Wrap(err, "list of DB Clusters empty")
	}

	return databaseClusters.DBClusters[0].DBClusterMembers, nil

}

func (d *DBInstance) getDatabaseInfo(client *rds.RDS) error {
	databaseInstances, err := client.DescribeDBInstances(&rds.DescribeDBInstancesInput{DBInstanceIdentifier: &d.DBInstanceIdentifier})
	if err != nil {
		return errors.Wrap(err, "unable to describe DB instance")
	}

	if len(databaseInstances.DBInstances) == 0 {
		return errors.Wrap(err, "list of DB instances empty")
	}
	(*d).DBInstanceStatus = *databaseInstances.DBInstances[0].DBInstanceStatus
	(*d).DBInstanceClass = *databaseInstances.DBInstances[0].DBInstanceClass
	(*d).DBClusterIdentifier = *databaseInstances.DBInstances[0].DBClusterIdentifier

	databaseClusters, err := client.DescribeDBClusters(&rds.DescribeDBClustersInput{DBClusterIdentifier: &d.DBClusterIdentifier})
	if err != nil {
		return errors.Wrap(err, "unable to describe the DB Cluster")
	}

	if len(databaseClusters.DBClusters) == 0 {
		return errors.Wrap(err, "list of DB Clusters empty")
	}
	for _, member := range databaseClusters.DBClusters[0].DBClusterMembers {
		if *member.DBInstanceIdentifier == d.DBInstanceIdentifier {
			(*d).IsClusterWriter = *member.IsClusterWriter
		}
	}
	return nil
}

func (d *DBInstance) getNewClassType() (string, error) {
	newClass, err := d.increaseSize()
	if err != nil {
		return "", err
	}
	log.Infof("New DB instance class (%s)", newClass)
	return newClass, nil
}

func (d *DBInstance) getSetDBInstanceClass() bool {
	for i, dbClass := range DBInstanceClasses {
		if d.DBInstanceClass == dbClass {
			(*d).SizeIndex = i
			return true
		}
	}
	return false
}

func (d DBInstance) increaseSize() (string, error) {
	newIndex := d.SizeIndex + 1
	if (newIndex + 1) >= len(DBInstanceClasses) {
		return "", errors.Errorf("Maximum instance size used. Index out of range")
	}
	return DBInstanceClasses[newIndex], nil
}

func (d *DBInstance) changeDatabaseClass(client *rds.RDS, dbInstanceClass string) error {
	modifyDBInstanceInput := &rds.ModifyDBInstanceInput{
		ApplyImmediately:     aws.Bool(true),
		DBInstanceClass:      aws.String(dbInstanceClass),
		DBInstanceIdentifier: aws.String(d.DBInstanceIdentifier),
	}
	log.Infof("Upgrading database (%s) to class (%s)", d.DBInstanceIdentifier, dbInstanceClass)
	_, err := client.ModifyDBInstance(modifyDBInstanceInput)
	if err != nil {
		return errors.Wrap(err, "unable to upgrade database to new class")
	}
	wait := 300
	log.Infof("Waiting up to %d seconds for db instance to start modifications...", wait)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(wait)*time.Second)
	defer cancel()
	err = d.waitForDBInstanceStartModifications(ctx, client)
	if err != nil {
		return err
	}

	wait = 1000
	log.Infof("Waiting up to %d seconds for db instance to become available...", wait)
	ctx, cancel = context.WithTimeout(context.Background(), time.Duration(wait)*time.Second)
	defer cancel()
	err = d.waitForDBInstanceReady(ctx, client)
	if err != nil {
		return err
	}

	return nil
}

func (d *DBInstance) waitForDBInstanceReady(ctx context.Context, client *rds.RDS) error {
	for {
		select {
		case <-ctx.Done():
			return errors.Wrap(ctx.Err(), "timed out waiting for database to get ready")
		default:
			var shouldWait bool

			databaseInstances, err := client.DescribeDBInstances(&rds.DescribeDBInstancesInput{DBInstanceIdentifier: &d.DBInstanceIdentifier})
			if err != nil {
				log.WithError(err).Error("unable to describe DB instance")
			}

			if len(databaseInstances.DBInstances) == 0 {
				log.Error("List of DB instances empty")
			} else {
				if *databaseInstances.DBInstances[0].DBInstanceStatus != "available" {
					shouldWait = true
					(*d).DBInstanceStatus = *databaseInstances.DBInstances[0].DBInstanceStatus
					break
				}

				if !shouldWait {
					(*d).DBInstanceStatus = *databaseInstances.DBInstances[0].DBInstanceStatus
					log.Infof("DB instance (%s) status (%s)", d.DBInstanceIdentifier, d.DBInstanceStatus)
					return nil
				}
			}

			time.Sleep(15 * time.Second)
		}
	}
}

func (d *DBInstance) waitForDBInstanceStartModifications(ctx context.Context, client *rds.RDS) error {
	for {
		select {
		case <-ctx.Done():
			return errors.Wrap(ctx.Err(), "timed out waiting for datatabase modifications to begin")
		default:
			var shouldWait bool

			databaseInstances, err := client.DescribeDBInstances(&rds.DescribeDBInstancesInput{DBInstanceIdentifier: &d.DBInstanceIdentifier})
			if err != nil {
				log.WithError(err).Error("unable to describe DB instance")
			}

			if len(databaseInstances.DBInstances) == 0 {
				log.Error("List of DB instances empty")
			} else {
				if *databaseInstances.DBInstances[0].DBInstanceStatus == "available" {
					shouldWait = true
					(*d).DBInstanceStatus = *databaseInstances.DBInstances[0].DBInstanceStatus
					break
				}

				if !shouldWait {
					(*d).DBInstanceStatus = *databaseInstances.DBInstances[0].DBInstanceStatus
					log.Infof("DB instance (%s) status (%s)", d.DBInstanceIdentifier, d.DBInstanceStatus)
					return nil
				}
			}

			time.Sleep(5 * time.Second)
		}
	}
}

func (d *DBInstance) databaseFailover(client *rds.RDS) error {
	_, err := client.FailoverDBCluster(&rds.FailoverDBClusterInput{
		DBClusterIdentifier:        &d.DBClusterIdentifier,
		TargetDBInstanceIdentifier: &d.DBInstanceIdentifier,
	})
	if err != nil {
		return errors.Wrap(err, "unable to failover DB cluster")
	}
	return nil
}
