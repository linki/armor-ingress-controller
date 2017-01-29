package main

import (
	"os"
	"time"

	log "github.com/Sirupsen/logrus"
	"gopkg.in/alecthomas/kingpin.v2"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/labels"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/linki/armor-ingress-controller/controller"
)

const (
	// the namespace where armor and armor-controller run
	namespace = "kube-system"

	// the name of the ConfigMap used by armor, must live in `namespace`
	configMapName = "armor-config"

	// the name of the key to use inside the ConfigMap
	configMapKey = "armor.json"

	// the name of the DaemonSet runnign armor
	daemonSetName = "armor"

	// the version of this tool
	version = "unknown"
)

var (
	// path to a kubeconfig file
	kubeconfig string

	// defines whether to find the cluster from the environment
	inCluster bool

	// the interval between synchronizations
	interval time.Duration

	// exit after first synchronization
	once bool

	// increase log output
	debug bool
)

func init() {
	kingpin.Flag("kubeconfig", "Path to a kubeconfig file.").Default(clientcmd.RecommendedHomeFile).StringVar(&kubeconfig)
	kingpin.Flag("in-cluster", "If true, finds the Kubernetes cluster from the environment.").BoolVar(&inCluster)
	kingpin.Flag("interval", "Interval between Ingresses are synchronized with Armor.").Default("1m").DurationVar(&interval)
	kingpin.Flag("once", "Run once and exit.").BoolVar(&once)
	kingpin.Flag("debug", "Enable debug logging.").BoolVar(&debug)
}

func main() {
	kingpin.Version(version)
	kingpin.Parse()

	if debug {
		log.SetLevel(log.DebugLevel)
	}

	client, err := newClient()
	if err != nil {
		log.Fatal(err)
	}

	controller := controller.NewController(client)

	for {
		// get all Ingress objects.
		ingresses, err := controller.GetIngresses()
		if err != nil {
			log.Fatal(err)
		}

		// generate an Armor config from the ingress list.
		config, err := controller.GenerateConfig(ingresses...)
		if err != nil {
			log.Fatal(err)
		}

		// create a ConfigMap for the Armor config if it doesn't exist.
		if err = controller.EnsureConfigMap(namespace, configMapName); err != nil {
			log.Fatal(err)
		}

		// write the Armor config to the ConfigMap.
		if err = controller.WriteConfigToConfigMapByName(config, namespace, configMapName, configMapKey); err != nil {
			log.Fatal(err)
		}

		// Update the definition of the Armor DaemonSet.
		daemonSet, err := controller.UpdateDaemonSetByName(namespace, daemonSetName, config)
		if err != nil {
			log.Fatal(err)
		}

		// Force all members of the DaemonSet to be recreated to apply the new config.
		labelSet := labels.Set(daemonSet.Spec.Selector.MatchLabels)

		if err = controller.UpdatePodsByLabelSelector(namespace, labelSet.AsSelector(), config); err != nil {
			log.Fatal(err)
		}

		// Get the public IPs of all nodes.
		ingressIPs, err := controller.GetNodeIPs()
		if err != nil {
			log.Fatal(err)
		}

		// Update all Ingress objects with the public IPs of all nodes.
		if err := controller.UpdateIngressLoadBalancers(ingresses, ingressIPs...); err != nil {
			log.Fatal(err)
		}

		if once {
			os.Exit(0)
		}

		select {
		case <-time.After(interval):
			continue
		}
	}
}

// newClient returns a kubernetes client that points to either the current
// context's cluster in the configured kubeconfig file or to the cluster that
// this binary runs in.
func newClient() (*kubernetes.Clientset, error) {
	var (
		config *rest.Config
		err    error
	)

	if inCluster {
		config, err = rest.InClusterConfig()
		log.Debugf("Using in-cluster config.")
	} else {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		log.Debugf("Using current context from kubeconfig at %s.", kubeconfig)
	}
	if err != nil {
		return nil, err
	}
	log.Infof("Using cluster at %s", config.Host)

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return client, nil
}
