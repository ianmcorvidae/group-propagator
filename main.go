package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/cyverse-de/configurate"
	"github.com/cyverse-de/go-mod/otelutils"
	"github.com/cyverse-de/messaging/v9"

	"github.com/cyverse-de/group-propagator/client/groups"
	"github.com/cyverse-de/group-propagator/logging"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"github.com/streadway/amqp"
)

var log = logging.Log.WithFields(logrus.Fields{"package": "main"})

const serviceName = "group-propagator"

//const otelName = "github.com/cyverse-de/group-propagator"

func getQueueName(prefix string) string {
	if len(prefix) > 0 {
		return fmt.Sprintf("%s.%s", prefix, serviceName)
	}
	return serviceName
}

// A spinner to keep the program running since client.Listen() needs to be in a goroutine.
//nolint
func spin() {
	spinner := make(chan int)
	for {
		select {
		case <-spinner:
			fmt.Println("Exiting")
			break
		}
	}
}

func main() {
	var (
		cfgPath  = flag.String("config", "/etc/iplant/de/group-propagator.yml", "The path to the config file")
		logLevel = flag.String("log-level", "info", "One of trace, debug, info, warn, error, fatal, or panic.")

		err error
		cfg *viper.Viper
	)

	flag.Parse()
	logging.SetupLogging(*logLevel)

	var tracerCtx, cancel = context.WithCancel(context.Background())
	defer cancel()
	shutdown := otelutils.TracerProviderFromEnv(tracerCtx, serviceName, func(e error) { log.Fatal(e) })
	defer shutdown()

	if *cfgPath == "" {
		log.Fatal("--config must not be the empty string")
	}

	if cfg, err = configurate.Init(*cfgPath); err != nil {
		log.Fatal(err.Error())
	}

	// package up config nicely

	// Set up AMQP
	listenClient, err := messaging.NewClient(cfg.GetString("amqp.uri"), true)
	if err != nil {
		log.Fatal(errors.Wrap(err, "Unable to create the messaging listen client"))
	}
	defer listenClient.Close()

	publishClient, err := messaging.NewClient(cfg.GetString("amqp.uri"), true)
	if err != nil {
		log.Fatal(errors.Wrap(err, "Unable to create the messaging publish client"))
	}
	defer publishClient.Close()

	err = publishClient.SetupPublishing(cfg.GetString("amqp.exchange.name"))
	if err != nil {
		log.Fatal(errors.Wrap(err, "Unable to set up message publishing"))
	}

	go listenClient.Listen()

	gn := "iplant:de:qa:users:de-users"
	gc := groups.NewGroupsClient(cfg.GetString("iplant_groups.base"), cfg.GetString("iplant_groups.user"))
	g, err := gc.GetGroupByName(context.Background(), gn)
	if err != nil {
		log.Fatal(errors.Wrap(err, "Unable to get group"))
	}
	log.Infof("%+v", g)

	queueName := getQueueName(cfg.GetString("amqp.queue_prefix"))
	listenClient.AddConsumerMulti(
		cfg.GetString("amqp.exchange.name"),
		cfg.GetString("amqp.exchange.type"),
		queueName,
		[]string{"index.all", "index.groups", "index.group.#"},
		func(ctx context.Context, del amqp.Delivery) {
			var err error
			log.Tracef("Got message: %s", del.RoutingKey)
			if del.RoutingKey == "index.all" || del.RoutingKey == "index.groups" {
				// crawl grouper, send incremental messages for each group
				// also crawl irods for deleted groups
			} else if strings.HasPrefix(del.RoutingKey, "index.group.") {
				// update/propagate a single group into irods
			}
			err = del.Ack(false)
			if err != nil {
				log.Error(errors.Wrap(err, fmt.Sprintf("Error acknowledging message: %s", del.RoutingKey)))
			}
		},
		1)

	spin() // unless we want to add an HTTP API of some sort, maybe
}
