package command

import (
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/Shopify/sarama"
	"github.com/funkygao/gafka/ctx"
	"github.com/funkygao/gafka/zk"
	"github.com/funkygao/gocli"
	"github.com/funkygao/golib/color"
)

type Clusters struct {
	Ui                cli.Ui
	Cmd               string
	verbose           bool
	registeredBrokers bool
}

// TODO cluster info will contain desciption,owner,etc.
func (this *Clusters) Run(args []string) (exitCode int) {
	var (
		addCluster  string
		setMode     bool
		verifyMode  bool
		clusterName string
		clusterPath string
		zone        string
		priority    int
		replicas    int
		addBroker   string
		nickname    string
		delBroker   int
	)
	cmdFlags := flag.NewFlagSet("clusters", flag.ContinueOnError)
	cmdFlags.Usage = func() { this.Ui.Output(this.Help()) }
	cmdFlags.StringVar(&zone, "z", "", "")
	cmdFlags.StringVar(&addCluster, "add", "", "")
	cmdFlags.BoolVar(&setMode, "s", false, "")
	cmdFlags.StringVar(&clusterName, "c", "", "")
	cmdFlags.StringVar(&clusterPath, "p", "", "")
	cmdFlags.BoolVar(&this.verbose, "l", false, "")
	cmdFlags.IntVar(&replicas, "replicas", -1, "")
	cmdFlags.IntVar(&priority, "priority", -1, "")
	cmdFlags.StringVar(&addBroker, "addbroker", "", "")
	cmdFlags.StringVar(&nickname, "nickname", "", "")
	cmdFlags.IntVar(&delBroker, "delbroker", -1, "")
	cmdFlags.BoolVar(&this.registeredBrokers, "r", false, "")
	cmdFlags.BoolVar(&verifyMode, "verify", false, "")
	if err := cmdFlags.Parse(args); err != nil {
		return 1
	}

	if validateArgs(this, this.Ui).
		on("-add", "-z", "-c", "-p").
		on("-s", "-z", "-c").
		requireAdminRights("-s", "-add").
		invalid(args) {
		return 2
	}

	if addCluster != "" {
		// add cluster
		zkzone := zk.NewZkZone(zk.DefaultConfig(zone, ctx.ZoneZkAddrs(zone)))
		if err := zkzone.RegisterCluster(addCluster, clusterPath); err != nil {
			this.Ui.Error(err.Error())
			return 1
		}

		this.Ui.Info(fmt.Sprintf("%s: %s created", addCluster, clusterPath))

		return
	}

	if setMode {
		// setup a cluser meta info
		zkzone := zk.NewZkZone(zk.DefaultConfig(zone, ctx.ZoneZkAddrs(zone)))
		zkcluster := zkzone.NewCluster(clusterName)
		switch {
		case priority != -1:
			zkcluster.SetPriority(priority)

		case replicas != -1:
			zkcluster.SetReplicas(replicas)

		case nickname != "":
			zkcluster.SetNickname(nickname)

		case addBroker != "":
			parts := strings.Split(addBroker, ":")
			if len(parts) != 3 {
				this.Ui.Output(this.Help())
				return
			}

			brokerId, err := strconv.Atoi(parts[0])
			swallow(err)
			port, err := strconv.Atoi(parts[2])
			swallow(err)
			err = zkcluster.RegisterBroker(brokerId, parts[1], port)
			swallow(err)

		case delBroker != -1:
			this.Ui.Error("not implemented yet")

		default:
			this.Ui.Error("command not recognized")
		}

		return
	}

	if verifyMode {
		if zone != "" {
			zkzone := zk.NewZkZone(zk.DefaultConfig(zone, ctx.ZoneZkAddrs(zone)))
			this.verifyBrokers(zkzone)
		} else {
			// print all zones all clusters
			forSortedZones(func(zkzone *zk.ZkZone) {
				this.verifyBrokers(zkzone)
			})
		}
		return
	}

	// display mode
	if zone != "" {
		zkzone := zk.NewZkZone(zk.DefaultConfig(zone, ctx.ZoneZkAddrs(zone)))
		this.printClusters(zkzone)
	} else {
		// print all zones all clusters
		forSortedZones(func(zkzone *zk.ZkZone) {
			this.printClusters(zkzone)
		})
	}

	return
}

func (this *Clusters) printRegisteredBrokers(zkzone *zk.ZkZone) {
	this.Ui.Output(zkzone.Name())
	zkzone.ForSortedClusters(func(zkcluster *zk.ZkCluster) {
		info := zkcluster.RegisteredInfo()
		this.Ui.Output(fmt.Sprintf("    %s(%s)", info.Name(), info.Nickname))
		registeredBrokers := info.Roster
		if len(registeredBrokers) == 0 {
			this.Ui.Warn("        brokers not defined")
		} else {
			for _, b := range registeredBrokers {
				this.Ui.Output(fmt.Sprintf("        %2d %s", b.Id, b.Addr()))
			}
		}

	})
}

func (this *Clusters) verifyBrokers(zkzone *zk.ZkZone) {
	this.Ui.Output(zkzone.Name())
	zkzone.ForSortedBrokers(func(cluster string, liveBrokers map[string]*zk.BrokerZnode) {
		zkcluster := zkzone.NewCluster(cluster)
		registeredBrokers := zkcluster.RegisteredInfo().Roster

		// find diff between registeredBrokers and liveBrokers
		// loop1 find liveBrokers>registeredBrokers
		for _, broker := range liveBrokers {
			foundInRoster := false
			for _, b := range registeredBrokers {
				bid := strconv.Itoa(b.Id)
				if bid == broker.Id && broker.Addr() == b.Addr() {
					foundInRoster = true
					break
				}
			}

			if !foundInRoster {
				// should manually register the broker
				this.Ui.Output(strings.Repeat(" ", 4) +
					color.Green("+ gk clusters -z %s -s -n %s -addbroker %s:%s",
						zkzone.Name(), cluster, broker.Id, broker.Addr()))
			}
		}

		// loop2 find liveBrokers<registeredBrokers
		for _, b := range registeredBrokers {
			foundInLive := false
			for _, broker := range liveBrokers {
				bid := strconv.Itoa(b.Id)
				if bid == broker.Id && broker.Addr() == b.Addr() {
					foundInLive = true
					break
				}
			}

			if !foundInLive {
				// the broker is dead
				this.Ui.Output(strings.Repeat(" ", 4) +
					color.Red("broker %d %s is dead", b.Id, b.Addr()))
			}
		}
	})
}

func (this *Clusters) printClusters(zkzone *zk.ZkZone) {
	if this.registeredBrokers {
		this.printRegisteredBrokers(zkzone)
		return
	}

	type clusterInfo struct {
		name, path         string
		topicN, partitionN int
		err                string
		priority           int
		replicas           int
		brokerInfos        []zk.BrokerInfo
	}
	clusters := make([]clusterInfo, 0)
	zkzone.ForSortedClusters(func(zkcluster *zk.ZkCluster) {
		ci := clusterInfo{
			name: zkcluster.Name(),
			path: zkcluster.Chroot(),
		}
		if !this.verbose {
			clusters = append(clusters, ci)
			return
		}

		// verbose mode, will calculate topics and partition count
		brokerList := zkcluster.BrokerList()
		if len(brokerList) == 0 {
			ci.err = "no live brokers"
			clusters = append(clusters, ci)
			return
		}

		kfk, err := sarama.NewClient(brokerList, sarama.NewConfig())
		if err != nil {
			ci.err = err.Error()
			clusters = append(clusters, ci)
			return
		}
		topics, err := kfk.Topics()
		if err != nil {
			ci.err = err.Error()
			clusters = append(clusters, ci)
			return
		}
		partitionN := 0
		for _, topic := range topics {
			partitions, err := kfk.Partitions(topic)
			if err != nil {
				ci.err = err.Error()
				clusters = append(clusters, ci)
				continue
			}

			partitionN += len(partitions)
		}

		info := zkcluster.RegisteredInfo()
		clusters = append(clusters, clusterInfo{
			name:        zkcluster.Name(),
			path:        zkcluster.Chroot(),
			topicN:      len(topics),
			partitionN:  partitionN,
			replicas:    info.Replicas,
			priority:    info.Priority,
			brokerInfos: info.Roster,
		})
	})

	this.Ui.Output(fmt.Sprintf("%s: %d", zkzone.Name(), len(clusters)))
	if this.verbose {
		// 2 loop: 1. print the err clusters 2. print the good clusters
		for _, c := range clusters {
			if c.err == "" {
				continue
			}

			this.Ui.Output(fmt.Sprintf("%30s: %s %s", c.name, c.path,
				color.Red(c.err)))
		}

		// loop2
		for _, c := range clusters {
			if c.err != "" {
				continue
			}

			this.Ui.Output(fmt.Sprintf("%30s: %s",
				c.name, c.path))
			this.Ui.Output(strings.Repeat(" ", 4) +
				color.Blue("topics:%d partitions:%d replicas:%d priority:%d brokers:%+v",
					c.topicN, c.partitionN, c.replicas, c.priority, c.brokerInfos))
		}

		return
	}

	// not verbose mode
	for _, c := range clusters {
		this.Ui.Output(fmt.Sprintf("%30s: %s", c.name, c.path))
	}

}

func (*Clusters) Synopsis() string {
	return "Register kafka clusters to a zone"
}

func (this *Clusters) Help() string {
	help := fmt.Sprintf(`
Usage: %s clusters [options]

    Register kafka clusters to a zone

Options:

    -z zone
      Only print kafka clusters within this zone.

    -c cluster name
      The new kafka cluster name.

    -l
      Use a long listing format.

    -add cluster name
      Add a new kafka cluster into a zone.

    -p cluster zk path
      The new kafka cluster chroot path in Zookeeper.
      e,g. gk clusters -z prod -add foo -p /kafka

    -s
      Setup a cluster info.
    
    -priority n
      Set the priority of a cluster.

    -nickname name
      Set nickname of a cluster.
      e,g. gk clusters -z prod -c foo -s -nickname bar

    -replicas n
      Set the default replicas of a cluster.

    -addbroker id:host:port
      Register a permanent broker to a cluster.

    -delbroker id TODO
      Delete a broker from a cluster.

    -r
      Display registered permanent brokers info.

    -verify
      Verify that the online brokers are consistent with the registered brokers.

`, this.Cmd)
	return strings.TrimSpace(help)
}
