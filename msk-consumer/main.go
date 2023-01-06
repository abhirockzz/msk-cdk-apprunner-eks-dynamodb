package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/twmb/franz-go/pkg/kgo"

	sasl_aws "github.com/twmb/franz-go/pkg/sasl/aws"
)

const consumerGroupName = "msk-eks-app-consumer-group"

var mskBroker string
var topic string
var client *kgo.Client

func init() {

	mskBroker = os.Getenv("MSK_BROKER")
	if mskBroker == "" {
		log.Fatal("missing env var MSK_BROKER")
	}

	topic = os.Getenv("MSK_TOPIC")
	if mskBroker == "" {
		log.Fatal("missing env var MSK_TOPIC")
	}

	log.Println("MSK_BROKER", mskBroker)
	log.Println("MSK_TOPIC", topic)

	tlsDialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}}

	cfg, err := config.LoadDefaultConfig(context.Background(), config.WithRegion(region))
	if err != nil {
		log.Fatal("failed to load config", err.Error())
	}

	creds, err := cfg.Credentials.Retrieve(context.Background())
	if err != nil {
		log.Println("failed to retrieve credentials", err.Error())
	}
	opts := []kgo.Opt{
		kgo.SeedBrokers(strings.Split(mskBroker, ",")...),
		kgo.SASL(sasl_aws.ManagedStreamingIAM(func(ctx context.Context) (sasl_aws.Auth, error) {

			log.Println("got credentials from", creds.Source)

			return sasl_aws.Auth{
				AccessKey:    creds.AccessKeyID,
				SecretKey:    creds.SecretAccessKey,
				SessionToken: creds.SessionToken,
				UserAgent:    "franz-go/creds_test/v1.0.0",
			}, nil
		})),

		kgo.Dialer(tlsDialer.DialContext),
		kgo.ConsumeTopics(topic),
		kgo.ConsumerGroup(consumerGroupName),
		kgo.OnPartitionsAssigned(partitionsAssigned),
		kgo.OnPartitionsRevoked(partitionsRevoked),
		kgo.OnPartitionsLost(partitionsLost),
	}

	client, err = kgo.NewClient(opts...)
	if err != nil {
		log.Fatal(err)
	}

	table = os.Getenv("DYNAMODB_TABLE")
	if table == "" {
		log.Fatal("environment variable DYNAMODB_TABLE missing")
	}

	region = os.Getenv("AWS_REGION")
	if region == "" {
		log.Fatal("environment variable AWS_REGION missing")
	}

	log.Println("Table", table, "Region", region)

	dynamoDBClient = dynamodb.New(dynamodb.Options{Credentials: cfg.Credentials, Region: region})
}

var dynamoDBClient *dynamodb.Client
var table string
var region string

func main() {

	log.Println("<<<< MSK kafka go consumer >>>>")

	go func() {
		log.Println("starting consumer goroutine.....")
		for {
			//fetches := client.PollFetches(context.Background())
			err := client.Ping(context.Background())
			if err != nil {
				log.Fatal("ping failed", err)
			}
			log.Println("fetching records...")

			consumeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			fetches := client.PollRecords(consumeCtx, 0)

			if fetches.IsClientClosed() {
				log.Println("kgo kafka client closed")
				return
			}
			fetches.EachError(func(t string, p int32, err error) {
				log.Printf("fetch err topic %s partition %d: %v\n", t, p, err)
			})

			fetches.EachRecord(func(r *kgo.Record) {
				log.Printf("got record from partition %v key=%s val=%s\n", r.Partition, string(r.Key), string(r.Value))

				item := make(map[string]types.AttributeValue)

				var user User
				err := json.Unmarshal(r.Value, &user)
				if err != nil {
					log.Println("failed to unmarshal msk json payload", err)
				}

				item["email"] = &types.AttributeValueMemberS{Value: user.Email}
				item["name"] = &types.AttributeValueMemberS{Value: user.Name}

				_, err = dynamoDBClient.PutItem(context.Background(), &dynamodb.PutItemInput{
					TableName: aws.String(table),
					Item:      item})

				if err != nil {
					log.Println("dynamodb put item failed", err)
				} else {
					log.Println("saved item to dynamodb table", table)
				}

			})

			log.Println("committing offsets")

			err = client.CommitUncommittedOffsets(context.Background())
			if err != nil {
				log.Printf("commit records failed: %v\n", err)
				continue
			}
		}
	}()

	end := make(chan os.Signal, 1)
	signal.Notify(end, syscall.SIGINT, syscall.SIGTERM)

	<-end
	log.Println("end initiated, closing client")

	client.Close()
	log.Println("program ended")
}

func partitionsAssigned(ctx context.Context, c *kgo.Client, m map[string][]int32) {
	log.Printf("paritions ASSIGNED for topic %s %v\n", topic, m[topic])
}

func partitionsRevoked(ctx context.Context, c *kgo.Client, m map[string][]int32) {
	log.Printf("paritions REVOKED for topic %s %v\n", topic, m[topic])
}

func partitionsLost(ctx context.Context, c *kgo.Client, m map[string][]int32) {
	log.Printf("paritions LOST for topic %s %v\n", topic, m[topic])
}

type User struct {
	Email string
	Name  string
}
