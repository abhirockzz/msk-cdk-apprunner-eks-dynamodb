package main

import (
	"fmt"
	"log"
	"os"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsdynamodb"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsec2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsecrassets"
	"github.com/aws/aws-cdk-go/awscdk/v2/awseks"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsmsk"
	"github.com/aws/aws-cdk-go/awscdkapprunneralpha/v2"
	"github.com/aws/jsii-runtime-go"

	"github.com/aws/constructs-go/constructs/v10"
)

const tableName = "users"
const dynamoDBPartitionKey = "email"
const appPort = 8080
const mskConsumerAppDir = "../msk-consumer"
const mskProducerAppDir = "../msk-producer"
const mskTopicPolicyStatementNameFormat = "arn:aws:kafka:%s:%s:topic/%s/*"

var mskTopicName string
var mskClusterEndpoint string
var mskCluster awsmsk.CfnServerlessCluster

func init() {
	mskTopicName = os.Getenv("MSK_TOPIC")
	if mskTopicName == "" {
		mskTopicName = "test-topic"
		log.Println("missing env var MSK_TOPIC. using test-topic as topic name")
	}

	log.Println("MSK TOPIC", mskTopicName)

	mskClusterEndpoint = os.Getenv("MSK_BROKER")

	if mskClusterEndpoint != "" {
		log.Println("MSK_BROKER", mskClusterEndpoint)
	}
}

type CdktackProps struct {
	awscdk.StackProps
}

func main() {
	app := awscdk.NewApp(nil)
	Stack1(app, "MSKDynamoDBEKSInfraStack", &CdktackProps{
		awscdk.StackProps{
			Env: env(),
		},
	})

	Stack2(app, "AppRunnerServiceStack", &CdktackProps{
		awscdk.StackProps{
			Env: env(),
		},
	})

	app.Synth(nil)
}

var vpc awsec2.Vpc
var securityGroup awsec2.SecurityGroup

func Stack1(scope constructs.Construct, id string, props *CdktackProps) awscdk.Stack {

	var sprops awscdk.StackProps
	if props != nil {
		sprops = props.StackProps
	}
	stack := awscdk.NewStack(scope, &id, &sprops)

	//vpc
	vpc = awsec2.NewVpc(stack, jsii.String("demo-vpc"), nil)

	securityGroup = awsec2.NewSecurityGroup(stack, jsii.String("demosg"),
		&awsec2.SecurityGroupProps{
			Vpc:               vpc,
			SecurityGroupName: jsii.String("demo-sg"),
			AllowAllOutbound:  jsii.Bool(true)})

	// self access
	awsec2.NewCfnSecurityGroupIngress(stack, jsii.String("self ingress"),
		&awsec2.CfnSecurityGroupIngressProps{
			GroupId:               securityGroup.SecurityGroupId(),
			SourceSecurityGroupId: securityGroup.SecurityGroupId(),
			Description:           jsii.String("self access"),
			IpProtocol:            jsii.String("-1"),
		})

	var vpcPrivateSubnetIDs []*string

	for _, subnet := range *vpc.PrivateSubnets() {
		vpcPrivateSubnetIDs = append(vpcPrivateSubnetIDs, subnet.SubnetId())
	}

	mskCluster = awsmsk.NewCfnServerlessCluster(stack, jsii.String("msk-serverless-cluster"),
		&awsmsk.CfnServerlessClusterProps{
			ClusterName: jsii.String("msk-serverless-cdk"),
			ClientAuthentication: awsmsk.CfnServerlessCluster_ClientAuthenticationProperty{
				Sasl: awsmsk.CfnServerlessCluster_SaslProperty{
					Iam: awsmsk.CfnServerlessCluster_IamProperty{
						Enabled: true}},
			},
			VpcConfigs: []awsmsk.CfnServerlessCluster_VpcConfigProperty{{
				SubnetIds:      &vpcPrivateSubnetIDs,
				SecurityGroups: jsii.Strings(*securityGroup.SecurityGroupId()),
			}},
		})

	mskCluster.ApplyRemovalPolicy(awscdk.RemovalPolicy_DESTROY, nil)

	//--------EKS---------

	eksCluster := awseks.NewCluster(stack, jsii.String("demo-eks"),
		&awseks.ClusterProps{
			ClusterName:   jsii.String("demo-eks-cluster"),
			Version:       awseks.KubernetesVersion_V1_21(),
			Vpc:           vpc,
			SecurityGroup: securityGroup,
			VpcSubnets: &[]*awsec2.SubnetSelection{
				{Subnets: vpc.PrivateSubnets()}},
			DefaultCapacity:         jsii.Number(2),
			DefaultCapacityInstance: awsec2.InstanceType_Of(awsec2.InstanceClass_BURSTABLE3, awsec2.InstanceSize_SMALL), DefaultCapacityType: awseks.DefaultCapacityType_NODEGROUP,
			OutputConfigCommand: jsii.Bool(true),
			EndpointAccess:      awseks.EndpointAccess_PUBLIC()})

	// for EKS to acccess MSK

	awsec2.NewCfnSecurityGroupIngress(stack, jsii.String("eks-msk-ingress"),
		&awsec2.CfnSecurityGroupIngressProps{
			GroupId:               securityGroup.SecurityGroupId(),
			SourceSecurityGroupId: eksCluster.ClusterSecurityGroupId(),
			Description:           jsii.String("for EKS to access MSK"),
			IpProtocol:            jsii.String("tcp"),
			FromPort:              jsii.Number(9098),
			ToPort:                jsii.Number(9098),
		})

	awsdynamodb.NewTable(stack, jsii.String("dynamodb-table"),
		&awsdynamodb.TableProps{
			TableName: jsii.String(tableName),
			PartitionKey: &awsdynamodb.Attribute{
				Name: jsii.String(dynamoDBPartitionKey),
				Type: awsdynamodb.AttributeType_STRING,
			},
			BillingMode:   awsdynamodb.BillingMode_PAY_PER_REQUEST,
			RemovalPolicy: awscdk.RemovalPolicy_DESTROY,
		})

	return stack
}

func Stack2(scope constructs.Construct, id string, props *CdktackProps) awscdk.Stack {
	var sprops awscdk.StackProps
	if props != nil {
		sprops = props.StackProps
	}
	stack := awscdk.NewStack(scope, &id, &sprops)

	mskProducerAppImage := awsecrassets.NewDockerImageAsset(stack, jsii.String("app-image"),
		&awsecrassets.DockerImageAssetProps{
			Directory: jsii.String(mskProducerAppDir)})

	apprunnerInstanceRole := awsiam.NewRole(stack, jsii.String("role-apprunner-msk"),
		&awsiam.RoleProps{
			AssumedBy: awsiam.NewServicePrincipal(jsii.String("tasks.apprunner.amazonaws.com"), nil),
		})

	topicResourceInPolicy := fmt.Sprintf(mskTopicPolicyStatementNameFormat, *stack.Region(), *stack.Account(), *mskCluster.ClusterName())

	apprunnerInstanceRole.AddToPolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Effect: awsiam.Effect_ALLOW,
		Actions: jsii.Strings(
			"kafka-cluster:Connect",
			"kafka-cluster:*Topic*",
			"kafka-cluster:WriteData"),
		Resources: jsii.Strings(
			*mskCluster.AttrArn(),
			topicResourceInPolicy,
		)}))

	app := awscdkapprunneralpha.NewService(stack, jsii.String("apprunner-msk-producer"),
		&awscdkapprunneralpha.ServiceProps{
			Source: awscdkapprunneralpha.NewAssetSource(
				&awscdkapprunneralpha.AssetProps{
					ImageConfiguration: &awscdkapprunneralpha.ImageConfiguration{Environment: &map[string]*string{
						"MSK_BROKER": jsii.String(mskClusterEndpoint),
						"MSK_TOPIC":  jsii.String(mskTopicName)},
						Port: jsii.Number(appPort)},
					Asset: mskProducerAppImage}),
			InstanceRole: apprunnerInstanceRole,
			Memory:       awscdkapprunneralpha.Memory_TWO_GB(),
			Cpu:          awscdkapprunneralpha.Cpu_ONE_VCPU(),
			VpcConnector: awscdkapprunneralpha.NewVpcConnector(stack, jsii.String("apprunner-msk-vpc"),
				&awscdkapprunneralpha.VpcConnectorProps{
					Vpc:              vpc,
					SecurityGroups:   &[]awsec2.ISecurityGroup{securityGroup},
					VpcConnectorName: jsii.String("msk-vpc"),
					VpcSubnets: &awsec2.SubnetSelection{
						Subnets: vpc.PrivateSubnets(),
					},
				}),
		})

	app.ApplyRemovalPolicy(awscdk.RemovalPolicy_DESTROY)

	appurl := "https://" + *app.ServiceUrl()
	awscdk.NewCfnOutput(stack, jsii.String("AppURL"), &awscdk.CfnOutputProps{Value: jsii.String(appurl)})

	mskConsumerAppImage := awsecrassets.NewDockerImageAsset(stack, jsii.String("msk-consumer-app-image"),
		&awsecrassets.DockerImageAssetProps{
			Directory: jsii.String(mskConsumerAppDir)})

	awscdk.NewCfnOutput(stack, jsii.String("ConsumerAppDockerImage"), &awscdk.CfnOutputProps{Value: mskConsumerAppImage.ImageUri()})

	return stack
}

// env determines the AWS environment (account+region) in which our stack is to
// be deployed. For more information see: https://docs.aws.amazon.com/cdk/latest/guide/environments.html
func env() *awscdk.Environment {
	return nil

}
