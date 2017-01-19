package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/labstack/armor"
	"github.com/mitchellh/hashstructure"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	extensions "k8s.io/client-go/pkg/apis/extensions/v1beta1"
	"k8s.io/client-go/pkg/labels"
)

const (
	ingressClassAnnotationKey   = "kubernetes.io/ingress.class"
	ingressClassAnnotationValue = "armor"
	configHashAnnotationKey     = "armor.labstack.com/configHash"
)

type Controller struct {
	Client kubernetes.Interface
}

func NewController(client kubernetes.Interface) *Controller {
	return &Controller{Client: client}
}

func (c *Controller) GetIngresses() ([]extensions.Ingress, error) {
	ingressList, err := c.Client.Extensions().Ingresses(v1.NamespaceAll).List(v1.ListOptions{})
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

func (c *Controller) GenerateConfig(ingresses ...extensions.Ingress) *armor.Armor {
	config := &armor.Armor{
		Hosts: make(map[string]*armor.Host),
	}

	config.TLS = &armor.TLS{
		Address:  ":443",
		Auto:     true,
		CacheDir: "/var/cache/armor",
	}

	config.Plugins = []armor.Plugin{
		armor.Plugin{"name": "logger"},
		armor.Plugin{"name": "https-redirect"},
	}

	for _, ingress := range ingresses {
		for _, rule := range ingress.Spec.Rules {
			// TODO(linki): is this needed or would that be an invalid ingress anyways?
			if rule.HTTP == nil {
				continue
			}

			targets := make([]map[string]string, 0, len(rule.HTTP.Paths))

			for _, path := range rule.HTTP.Paths {
				target := map[string]string{
					"url": upstreamServiceURL(ingress.Namespace,
						path.Backend.ServiceName, path.Backend.ServicePort.String()),
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

	return config
}

func (c *Controller) WriteConfigToConfigMapByName(config *armor.Armor, namespace, configMapName, key string) error {
	configMap, err := c.Client.Core().ConfigMaps(namespace).Get(configMapName)
	if err != nil {
		return err
	}

	return c.WriteConfigToConfigMap(config, configMap, key)
}

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

func (c *Controller) WriteConfigToWriter(config *armor.Armor, writer io.Writer) error {
	return json.NewEncoder(writer).Encode(config)
}

func (c *Controller) EnsureConfigMap(namespace, configMapName string) error {
	if _, err := c.Client.Core().ConfigMaps(namespace).Get(configMapName); err == nil {
		return nil
	}

	configMap := &v1.ConfigMap{
		ObjectMeta: v1.ObjectMeta{
			Namespace: namespace,
			Name:      configMapName,
		},
	}

	if _, err := c.Client.Core().ConfigMaps(namespace).Create(configMap); err != nil {
		return err
	}

	return nil
}

func (c *Controller) UpdateDeploymentByName(namespace string, deploymentName string, config *armor.Armor) error {
	deployment, err := c.Client.Extensions().Deployments(namespace).Get(deploymentName)
	if err != nil {
		return err
	}

	return c.UpdateDeployment(deployment, config)
}

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

func (c *Controller) UpdateDaemonSetByName(namespace string, daemonSetName string, config *armor.Armor) (*extensions.DaemonSet, error) {
	daemonSet, err := c.Client.Extensions().DaemonSets(namespace).Get(daemonSetName)
	if err != nil {
		return nil, err
	}

	return c.UpdateDaemonSet(daemonSet, config)
}

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

func (c *Controller) UpdatePodsByLabelSelector(namespace string, selector labels.Selector, config *armor.Armor) error {
	configHash := getConfigHash(config)

	listOptions := v1.ListOptions{LabelSelector: selector.String()}

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

func (c *Controller) GetNodeIPs() ([]string, error) {
	nodeIPs := []string{}

	nodes, err := c.Client.Core().Nodes().List(v1.ListOptions{})
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

// TODO(linki): don't panic on error here
func getConfigHash(config *armor.Armor) string {
	hash, err := hashstructure.Hash(config, nil)
	if err != nil {
		panic(err)
	}

	return strconv.Itoa(int(hash))
}

// TODO(linki): check if pointing to cluster service IP is better
func upstreamServiceURL(namespace, name, port string) string {
	return fmt.Sprintf("http://%s.%s.svc:%s", name, namespace, port)
}
