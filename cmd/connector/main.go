package main

import (
	"fmt"
	"net/http"

	"github.com/opsee/bastion/config"
	"github.com/opsee/bastion/connector"
	"github.com/opsee/bastion/logging"
	"github.com/opsee/bastion/messaging"
)

var (
	log = logging.GetLogger("bastion")
)

func main() {
	configuration := config.GetConfig()
	fmt.Println("config", configuration)
	httpClient := &http.Client{}
	mdp := config.NewMetadataProvider(httpClient, configuration)
	connector := connector.StartConnector(configuration.Opsee, 1000, 1000, mdp.Get(), configuration)
	msg := <-connector.Recv
	fmt.Println("registration acknowledged", msg)
	cmdProducer, err := messaging.NewProducer("commands")
	if err != nil {
		log.Error(err.Error())
		return
	}
	replyConsumer, err := messaging.NewConsumer("replies", "connector")
	if err != nil {
		log.Error(err.Error())
		return
	}
	go processCommands(connector, cmdProducer)
	go processReplies(connector, replyConsumer)
}

func processCommands(connector *connector.Connector, cmdProducer *messaging.Producer) {
	for event := range connector.Recv {
		id := connector.DeferReply(event)
		cmdProducer.PublishRepliable(id, event)
	}
}

func processReplies(connector *connector.Connector, replyConsumer *messaging.Consumer) {
	for event := range replyConsumer.Channel() {
		id := event.ReplyTo
		connector.DoReply(id, event)
	}
}
