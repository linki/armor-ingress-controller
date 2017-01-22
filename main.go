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
	namespace     = "kube-system"
	configMapName = "armor-config"
	configMapKey  = "armor.json"
	daemonSetName = "armor"
	version       = "unknown"
)

var (
	kubeconfig string
	inCluster  bool
	interval   time.Duration
	once       bool
	debug      bool
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
		ingresses, err := controller.GetIngresses()
		if err != nil {
			log.Fatal(err)
		}

		config := controller.GenerateConfig(ingresses...)

		if err = controller.EnsureConfigMap(namespace, configMapName); err != nil {
			log.Fatal(err)
		}

		// controller.WriteConfigToWriter(config, os.Stdout)

		if err = controller.WriteConfigToConfigMapByName(config, namespace, configMapName, configMapKey); err != nil {
			log.Fatal(err)
		}

		daemonSet, err := controller.UpdateDaemonSetByName(namespace, daemonSetName, config)
		if err != nil {
			log.Fatal(err)
		}

		labelSet := labels.Set(daemonSet.Spec.Selector.MatchLabels)

		if err = controller.UpdatePodsByLabelSelector(namespace, labelSet.AsSelector(), config); err != nil {
			log.Fatal(err)
		}

		ingressIPs, err := controller.GetNodeIPs()
		if err != nil {
			log.Fatal(err)
		}

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
