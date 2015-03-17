package main

import (
	"bastion/credentials"
	"bastion/netutil"
	"bastion/scanner"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"time"
	// "bastion/resilient"
	"encoding/json"
	"github.com/amir/raidman"
	"github.com/awslabs/aws-sdk-go/gen/ec2"
	"github.com/awslabs/aws-sdk-go/gen/elb"
	"github.com/awslabs/aws-sdk-go/gen/rds"
	"github.com/op/go-logging"
	"runtime"
	"strconv"
	"strings"
)

var (
	log       = logging.MustGetLogger("bastion.json-tcp")
	logFormat = logging.MustStringFormatter("%{time:2006-01-02T15:04:05.999999999Z07:00} %{level} [%{module}] %{message}")
)

// we must first retrieve our AWS API keys, which will either be in the instance metadata,
// or our command line options. Then we begin scanning the environment, first using the AWS
// API, and then actually trying to open TCP connections.

// In parallel we try and open a TLS connection back to the opsee API. We'll have been supplied
// a ca certificate, certificate and a secret key in pem format, either via the instance metadata
// or on the command line.
var (
	accessKeyId string
	secretKey   string
	region      string
	opsee       string
	caPath      string
	certPath    string
	keyPath     string
	dataPath    string
	hostname    string
)

func init() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	logging.SetLevel(logging.INFO, "json-tcp")
	logging.SetFormatter(logFormat)

	// cmdline args
	flag.StringVar(&accessKeyId, "access_key_id", "", "AWS access key ID.")
	flag.StringVar(&secretKey, "secret_key", "", "AWS secret key ID.")
	flag.StringVar(&region, "region", "", "AWS Region.")
	flag.StringVar(&opsee, "opsee", "localhost:5556", "Hostname and port to the Opsee server.")
	flag.StringVar(&caPath, "ca", "ca.pem", "Path to the CA certificate.")
	flag.StringVar(&certPath, "cert", "cert.pem", "Path to the certificate.")
	flag.StringVar(&keyPath, "key", "key.pem", "Path to the key file.")
	flag.StringVar(&dataPath, "data", "", "Data path.")
	flag.StringVar(&hostname, "hostname", "", "Hostname override.")
}

type Server struct{}

func (this *Server) SslOptions() netutil.SslOptions {
	return nil
}

func (this *Server) ConnectionMade(connection *netutil.Connection) bool {
	return true
}

func (this *Server) ConnectionLost(connection *netutil.Connection, err error) {
	log.Error("Connection lost: %v", err)
}

func (this *Server) RequestReceived(connection *netutil.Connection, request *netutil.ServerRequest) (reply *netutil.Reply, keepGoing bool) {
	isShutdown := request.Command == "shutdown"
	keepGoing = !isShutdown
	if isShutdown {
		if err := connection.Server().Close(); err != nil {
			log.Notice("shutdown")
			reply = nil
		}
	}
	reply = netutil.NewReply(request)
	return
}

type OpseeClient struct {
	*raidman.Client
}

func NewOpseeClient(address string) (client *raidman.Client, err error) {
	return raidman.Dial("tcp", address)
}

var (
	httpClient   *http.Client = &http.Client{}
	credProvider *credentials.CredentialsProvider
	jsonServer   netutil.TCPServer
	ec2Client    scanner.EC2Scanner
	opseeClient  *raidman.Client              = nil
	groupMap     map[string]ec2.SecurityGroup = make(map[string]ec2.SecurityGroup)
)

func main() {
	var err error
	flag.Parse()
	if jsonServer, err = netutil.ListenTCP(":5666", &Server{}); err != nil {
		log.Panic("json-tcp server failed to start: ", err)
		return
	}
	httpClient = &http.Client{}
	credProvider = credentials.NewProvider(httpClient, accessKeyId, secretKey, region)
	ec2Client = scanner.New(credProvider)
	connectToOpsee := netutil.NewBackoffRetrier(func() (interface{}, error) {
		return NewOpseeClient(opsee)
	})
	if err = connectToOpsee.Run(); err != nil {
		log.Fatalf("connectToOpsee: %v", err)
		return
	}
	opseeClient = connectToOpsee.Result().(*raidman.Client)

	if hostname == "" {
		if credProvider.GetInstanceId() == nil {
			hostname = "localhost"
		} else {
			hostname = credProvider.GetInstanceId().InstanceId
		}
	}
	log.Info("hostname: %s", hostname)

	go loadAndPopulate()
	connectionIdleLoop()
	jsonServer.Join()
}

func connectionIdleLoop() {
	tick := time.Tick(time.Second * 10)
	connectedEvent := &raidman.Event{
		State:   "connected",
		Host:    hostname,
		Service: "bastion",
		Ttl:     10}

	for {
		log.Info("%s", connectedEvent)
		opseeClient.Send(connectedEvent)
		<-tick
	}
}

func reportFromDataFile(dataFilePath string) (err error) {
	var events []raidman.Event
	var file *os.File
	var bytes []byte
	const sendTickInterval = time.Second * 5

	if file, err = os.Open(dataFilePath); err != nil {
		log.Panicf("opening data file %s: %v", dataPath, err)
	}
	if bytes, err = ioutil.ReadAll(file); err != nil {
		log.Panicf("reading from data file %s: %v", dataPath, err)
	}
	if err = json.Unmarshal(bytes, &events); err != nil {
		log.Panicf("unmarshalling json from %s: %v", dataPath, err)
	}
	discTick := time.Tick(sendTickInterval)
	for _, event := range events {
		<-discTick
		log.Debug("%v", event)
		opseeClient.Send(&event)
	}
	return
}

func loadAndPopulate() (err error) {
	if dataPath != "" {
		return reportFromDataFile(dataPath)
	}
	var groups []ec2.SecurityGroup
	if groups, err = ec2Client.ScanSecurityGroups(); err != nil {
		log.Fatalf("scanning security groups: %s", err.Error())
	}
	for _, group := range groups {
		if group.GroupID != nil {
			groupMap[*group.GroupID] = group
			instances, _ := ec2Client.ScanSecurityGroupInstances(*group.GroupID)
			if len(instances) == 0 {
				continue
			}
		} else {
			continue
		}
		event := ec2SecurityGroupToEvent(group)
		fmt.Println(event)
		opseeClient.Send(event)
	}
	lbs, _ := ec2Client.ScanLoadBalancers()
	for _, lb := range lbs {
		if lb.LoadBalancerName == nil {
			continue
		}
		event := elbLoadBalancerDescriptionToEvent(lb)
		fmt.Println(event)
		opseeClient.Send(event)
	}
	rdbs, _ := ec2Client.ScanRDS()
	sgs, _ := ec2Client.ScanRDSSecurityGroups()
	sgMap := make(map[string]rds.DBSecurityGroup)
	for _, sg := range sgs {
		if sg.DBSecurityGroupName != nil {
			sgMap[*sg.DBSecurityGroupName] = sg
		}
	}
	for _, db := range rdbs {
		event := rdsDBInstanceToEvent(db)
		fmt.Println(event)
		opseeClient.Send(event)
	}
	//FIN
	event := raidman.Event{}
	event.Ttl = 120
	event.Host = hostname
	event.Service = "discovery"
	event.State = "end"
	fmt.Println(event)
	opseeClient.Send(&event)
	return
}

func ec2SecurityGroupToEvent(group ec2.SecurityGroup) (event *raidman.Event) {
	event = &raidman.Event{Ttl: 120, Host: hostname, Service: "discovery", State: "sg", Metric: 0, Attributes: make(map[string]string)}
	event.Attributes["group_id"] = *group.GroupID
	if group.GroupName != nil {
		event.Attributes["group_name"] = *group.GroupName
	}
	if len(group.IPPermissions) > 0 {
		perms := group.IPPermissions[0]
		if perms.ToPort != nil {
			event.Attributes["port"] = strconv.Itoa(*perms.ToPort)
		}
		if perms.IPProtocol != nil {
			event.Attributes["protocol"] = *perms.IPProtocol
		}
	}
	return
}

func elbLoadBalancerDescriptionToEvent(lb elb.LoadBalancerDescription) (event *raidman.Event) {
	event = &raidman.Event{Ttl: 120, Host: hostname, Service: "discovery", State: "rds", Metric: 0, Attributes: make(map[string]string)}
	event.Attributes["group_name"] = *lb.LoadBalancerName
	event.Attributes["group_id"] = *lb.DNSName
	if lb.HealthCheck != nil {
		split := strings.Split(*lb.HealthCheck.Target, ":")
		split2 := strings.Split(split[1], "/")
		event.Attributes["port"] = split2[0]
		event.Attributes["protocol"] = split[0]
		event.Attributes["request"] = strings.Join([]string{"/", split2[1]}, "")
	}
	return
}

func rdsDBInstanceToEvent(db rds.DBInstance) (event *raidman.Event) {
	event = &raidman.Event{Ttl: 120, Host: hostname, Service: "discovery", State: "rds", Metric: 0, Attributes: make(map[string]string)}
	if db.DBName != nil {
		event.Attributes["group_name"] = *db.DBName
		if len(db.VPCSecurityGroups) > 0 {
			sgId := *db.VPCSecurityGroups[0].VPCSecurityGroupID
			event.Attributes["group_id"] = sgId
			ec2sg := groupMap[sgId]
			perms := ec2sg.IPPermissions[0]
			event.Attributes["port"] = strconv.Itoa(*perms.ToPort)
			event.Attributes["protocol"] = "sql"
			event.Attributes["request"] = "select 1;"
		}
	}
	return event
}
