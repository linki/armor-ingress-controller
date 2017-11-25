package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/labstack/armor"
	"github.com/mitchellh/hashstructure"
	log "github.com/sirupsen/logrus"

	"k8s.io/api/core/v1"
	extensions "k8s.io/api/extensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

const (
	// the annotation to use when specifying the responsible ingress controller
	// for an Ingress object. Commonly understood by other ingress controllers.
	ingressClassAnnotationKey = "kubernetes.io/ingress.class"

	// the value of the ingress class so that this controller feels responsible.
	ingressClassAnnotationValue = "armor"

	// the annotation key to use when marking the armor deployment with the
	// hash value of its current configuration. used to detect outdated pods.
	configHashAnnotationKey = "armor.labstack.com/configHash"
)

type Controller struct {
	// a kubernetes client object
	Client kubernetes.Interface
}

// NewController instantiates a new controller.
func NewController(client kubernetes.Interface) *Controller {
	return &Controller{Client: client}
}

// GetIngresses returns a slice of all Ingress objects that are annotated with
// this controller's annotation identifier.
func (c *Controller) GetIngresses() ([]extensions.Ingress, error) {
	ingressList, err := c.Client.Extensions().Ingresses(v1.NamespaceAll).List(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	ingresses := []extensions.Ingress{}

	for _, ingress := range ingressList.Items {
		if ingress.Annotations[ingressClassAnnotationKey] == ingressClassAnnotationValue {
			ingresses = append(ingresses, ingress)
		}
	}

	return ingresses, nil
}

// UpdateIngressLoadBalancers takes a slice of Ingress objects and updates the
// Status.LoadBalancer section with the given IPs.
func (c *Controller) UpdateIngressLoadBalancers(ingresses []extensions.Ingress, IPs ...string) error {
	loadBalancers := make([]v1.LoadBalancerIngress, 0, len(IPs))

	for _, ip := range IPs {
		loadBalancers = append(loadBalancers, v1.LoadBalancerIngress{IP: ip})
	}

	for _, ingress := range ingresses {
		ingress.Status.LoadBalancer.Ingress = loadBalancers

		if _, err := c.Client.Extensions().Ingresses(ingress.Namespace).UpdateStatus(&ingress); err != nil {
			return err
		}
	}

	return nil
}

// GenerateConfig receives a list of Ingress objects and generates a
// corresponding Armor config from it. It also sets up some default plugins and
// enables automatic TLS certificate retrieval from Let's Encrypt.
func (c *Controller) GenerateConfig(ingresses ...extensions.Ingress) (*armor.Armor, error) {
	config := &armor.Armor{
		Hosts: make(map[string]*armor.Host),
	}

	config.TLS = &armor.TLS{
		Address:  ":443",
		Auto:     true,
		CacheDir: "/var/cache/armor",
	}

	config.Plugins = []armor.Plugin{
		{"name": "logger"},
		{"name": "https-redirect"},
	}

	for _, ingress := range ingresses {
		for _, rule := range ingress.Spec.Rules {
			// TODO(linki): is this needed or would that be an invalid ingress anyways?
			if rule.HTTP == nil {
				continue
			}

			targets := make([]map[string]string, 0, len(rule.HTTP.Paths))

			for _, path := range rule.HTTP.Paths {
				service, err := c.Client.Core().Services(ingress.Namespace).Get(path.Backend.ServiceName, metav1.GetOptions{})
				if err != nil {
					if errors.IsNotFound(err) {
						log.Errorf("Can't find service '%s/%s' for ingress '%s/%s'",
							ingress.Namespace, path.Backend.ServiceName, ingress.Namespace, ingress.Name)
						continue
					}

					return nil, err
				}

				target := map[string]string{
					"url": upstreamServiceURL(service.Spec.ClusterIP, path.Backend.ServicePort.String()),
				}

				targets = append(targets, target)
			}

			proxy := armor.Plugin{
				"name":    "proxy",
				"targets": targets,
			}

			config.Hosts[rule.Host] = &armor.Host{
				Plugins: []armor.Plugin{proxy},
			}
		}
	}

	return config, nil
}

// WriteConfigToConfigMapByName updates a ConfigMap identified by namespace and
// name and stores the JSON representation of the passed in Armor config under
// the provided key.
func (c *Controller) WriteConfigToConfigMapByName(config *armor.Armor, namespace, configMapName, key string) error {
	configMap, err := c.Client.Core().ConfigMaps(namespace).Get(configMapName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	return c.WriteConfigToConfigMap(config, configMap, key)
}

// WriteConfigToConfigMap updates a passed in ConfigMap and stores the JSON
// representation of the passed in Armor config under the provided key.
// It also annotates the ConfigMap with the hash of its content.
func (c *Controller) WriteConfigToConfigMap(config *armor.Armor, configMap *v1.ConfigMap, key string) error {
	var buffer bytes.Buffer

	c.WriteConfigToWriter(config, &buffer)

	if configMap.Data == nil {
		configMap.Data = make(map[string]string)
	}

	configMap.Data[key] = buffer.String()

	if configMap.Annotations == nil {
		configMap.Annotations = make(map[string]string)
	}

	configMap.Annotations[configHashAnnotationKey] = getConfigHash(config)

	if _, err := c.Client.Core().ConfigMaps(configMap.Namespace).Update(configMap); err != nil {
		return err
	}

	return nil
}

// WriteConfigToWriter writes the JSON representation of an Armor config to a
// Writer.
func (c *Controller) WriteConfigToWriter(config *armor.Armor, writer io.Writer) error {
	return json.NewEncoder(writer).Encode(config)
}

// EnsureConfigMap creates a ConfigMap specified by namespace and name if it
// doesn't already exist.
func (c *Controller) EnsureConfigMap(namespace, configMapName string) error {
	if _, err := c.Client.Core().ConfigMaps(namespace).Get(configMapName, metav1.GetOptions{}); err == nil {
		return nil
	}

	configMap := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      configMapName,
		},
	}

	if _, err := c.Client.Core().ConfigMaps(namespace).Create(configMap); err != nil {
		return err
	}

	return nil
}

// UpdateDeploymentByName updates a Deployment identified by namespace and name.
func (c *Controller) UpdateDeploymentByName(namespace string, deploymentName string, config *armor.Armor) error {
	deployment, err := c.Client.Extensions().Deployments(namespace).Get(deploymentName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	return c.UpdateDeployment(deployment, config)
}

// UpdateDeployment updates the passed in Deployment. It updates the annotation
// in the Deployment as well as in the Pod template spec with the hash of the
// passed in config so that the deployment controller will update the pods.
//
// TODO(linki): tests with different namespace don't fail ??
func (c *Controller) UpdateDeployment(deployment *extensions.Deployment, config *armor.Armor) error {
	configHash := getConfigHash(config)

	if deployment.Annotations == nil {
		deployment.Annotations = make(map[string]string)
	}

	deployment.Annotations[configHashAnnotationKey] = configHash

	if deployment.Spec.Template.Annotations == nil {
		deployment.Spec.Template.Annotations = make(map[string]string)
	}

	deployment.Spec.Template.Annotations[configHashAnnotationKey] = configHash

	if _, err := c.Client.Extensions().Deployments(deployment.Namespace).Update(deployment); err != nil {
		return err
	}

	return nil
}

// UpdateDaemonSetByName updates a DaemonSet identified by namespace and name.
func (c *Controller) UpdateDaemonSetByName(namespace string, daemonSetName string, config *armor.Armor) (*extensions.DaemonSet, error) {
	daemonSet, err := c.Client.Extensions().DaemonSets(namespace).Get(daemonSetName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return c.UpdateDaemonSet(daemonSet, config)
}

// UpdateDaemonSet updates the passed in DaemonSet. It updates the annotation
// in the DaemonSet as well as in the Pod template spec with the hash of the
// passed in config. This won't update any pods immediately but newly created
// pods will correspond to the new template spec.
//
// TODO(linki): tests with different namespace don't fail ??
// TODO(linki): pods do not get updated correctly
func (c *Controller) UpdateDaemonSet(daemonSet *extensions.DaemonSet, config *armor.Armor) (*extensions.DaemonSet, error) {
	configHash := getConfigHash(config)

	if daemonSet.Annotations == nil {
		daemonSet.Annotations = make(map[string]string)
	}

	daemonSet.Annotations[configHashAnnotationKey] = configHash

	if daemonSet.Spec.Template.Annotations == nil {
		daemonSet.Spec.Template.Annotations = make(map[string]string)
	}

	daemonSet.Spec.Template.Annotations[configHashAnnotationKey] = configHash

	daemonSet, err := c.Client.Extensions().DaemonSets(daemonSet.Namespace).Update(daemonSet)
	if err != nil {
		return nil, err
	}

	return daemonSet, nil
}

// UpdatePodsByLabelSelector deletes all pods matching a provided label selector
// that are not annotated with the hash of the passed in config.
// When passed the label selector of a DaemonSet this can be used to force the
// pods to be updated.
func (c *Controller) UpdatePodsByLabelSelector(namespace string, selector labels.Selector, config *armor.Armor) error {
	configHash := getConfigHash(config)

	listOptions := metav1.ListOptions{LabelSelector: selector.String()}

	pods, err := c.Client.Core().Pods(namespace).List(listOptions)
	if err != nil {
		return err
	}

	for _, pod := range pods.Items {
		if pod.Annotations[configHashAnnotationKey] != configHash {
			if err := c.Client.Core().Pods(pod.Namespace).Delete(pod.Name, nil); err != nil {
				return err
			}
		}
	}

	return nil
}

// GetNodeIPs returns the public IPs of all nodes in the cluster.
func (c *Controller) GetNodeIPs() ([]string, error) {
	nodeIPs := []string{}

	nodes, err := c.Client.Core().Nodes().List(metav1.ListOptions{})
	if err != nil {
		return nodeIPs, err
	}

	for _, node := range nodes.Items {
		for _, address := range node.Status.Addresses {
			if address.Type == v1.NodeExternalIP {
				nodeIPs = append(nodeIPs, address.Address)
			}
		}
	}

	return nodeIPs, nil
}

// getConfigHash returns the hash value of the passed in config.
//
// TODO(linki): don't panic on error here
func getConfigHash(config *armor.Armor) string {
	hash, err := hashstructure.Hash(config, nil)
	if err != nil {
		panic(err)
	}

	return strconv.Itoa(int(hash))
}

// upstreamServiceURL returns an http URL given an IP and port.
func upstreamServiceURL(ip, port string) string {
	return fmt.Sprintf("http://%s:%s", ip, port)
}
