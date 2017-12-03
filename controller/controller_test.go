package controller

import (
	"bytes"
	"testing"

	"github.com/labstack/armor"
	"gopkg.in/yaml.v2"

	"k8s.io/api/core/v1"
	extensions "k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	testNamespace = "armor-test"
)

type Suite struct {
	suite.Suite
	client     kubernetes.Interface
	controller *Controller
	ingresses  []*extensions.Ingress
}

func (suite *Suite) SetupTest() {
	suite.client = fake.NewSimpleClientset()
	suite.controller = NewController(suite.client)
	// suite.ingresses = []*extensions.Ingress{}
}

func TestSuite(t *testing.T) {
	suite.Run(t, new(Suite))
}

func (suite *Suite) mockIngresses(testIngresses ...testIngress) []*extensions.Ingress {
	ingresses := []*extensions.Ingress{}

	for _, testIngress := range testIngresses {
		ingress := &extensions.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testIngress.name,
				Namespace: testIngress.namespace,
				Annotations: map[string]string{
					ingressClassAnnotationKey: testIngress.class,
				},
			},
		}

		ingress, err := suite.client.Extensions().Ingresses(testNamespace).Create(ingress)
		suite.Require().NoError(err)

		ingresses = append(ingresses, ingress)
	}

	return ingresses
}

// TODO(linki): make tests independent of ordering
func (suite *Suite) assertIngresses(ingresses []*extensions.Ingress, expected []testIngress) {
	suite.Require().Len(ingresses, len(expected))

	for i := range ingresses {
		suite.assertIngress(ingresses[i], expected[i])
	}
}

func (suite *Suite) assertIngress(ingress *extensions.Ingress, expected testIngress) {
	suite.Assert().Equal(expected.name, ingress.Name)
	suite.Assert().Equal(expected.namespace, ingress.Namespace)
	suite.Assert().Equal(expected.class, ingress.Annotations[ingressClassAnnotationKey])
}

func (suite *Suite) TestNew() {
	assert.Equal(suite.T(), suite.client, suite.controller.Client)
}

type testIngress struct {
	name      string
	namespace string
	class     string
}

func (suite *Suite) TestGetIngresses() {
	suite.mockIngresses(
		testIngress{name: "annotated", namespace: testNamespace, class: "armor"},
		testIngress{name: "not-annotated", namespace: testNamespace, class: ""},
	)

	ingresses, err := suite.controller.GetIngresses()
	suite.Assert().NoError(err)

	suite.assertIngresses(ingresses, []testIngress{
		// annotated ingresses are found.
		testIngress{name: "annotated", namespace: testNamespace, class: "armor"},
	})
}

func (suite *Suite) TestUpdateIngressLoadBalancer() {
	foo := testIngress{name: "foo", namespace: testNamespace, class: "armor"}
	bar := testIngress{name: "bar", namespace: testNamespace, class: "armor"}

	for _, ti := range []struct {
		title         string
		ingresses     []testIngress
		loadBalancers []string
	}{
		{
			title:         "it updates multiple ingresses with multiple IPs",
			ingresses:     []testIngress{foo, bar},
			loadBalancers: []string{"8.8.8.8", "8.8.4.4"},
		},
	} {
		suite.T().Run(ti.title, func(_ *testing.T) {
			ingresses := suite.mockIngresses(ti.ingresses...)

			err := suite.controller.UpdateIngressLoadBalancers(ingresses, ti.loadBalancers)
			suite.Assert().NoError(err)

			for _, ing := range ti.ingresses {
				ingress, err := suite.client.Extensions().Ingresses(ing.namespace).Get(ing.name, metav1.GetOptions{})
				suite.Require().NoError(err)

				assertLoadBalancerIPs(suite.T(), ingress, ti.loadBalancers)
			}
		})
	}
}

// test transformation from ingress objects to armor rules!

func (suite *Suite) TestGenerateConfig() {
	fixtures := []*extensions.Ingress{
		// main object under test
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "armor",
				Name:      "foo",
			},
			Spec: extensions.IngressSpec{
				Rules: []extensions.IngressRule{
					{
						Host: "foo.bar.com",
						IngressRuleValue: extensions.IngressRuleValue{
							HTTP: &extensions.HTTPIngressRuleValue{
								Paths: []extensions.HTTPIngressPath{
									{
										Backend: extensions.IngressBackend{
											ServiceName: "bar",
											ServicePort: intstr.FromInt(80),
										},
									},
									// valid? needed? two targets
									{
										Backend: extensions.IngressBackend{
											ServiceName: "baz",
											ServicePort: intstr.FromInt(8080),
										},
									},
									{
										Path: "/barbara",
										Backend: extensions.IngressBackend{
											ServiceName: "barbara",
											ServicePort: intstr.FromInt(9090),
										},
									},
								},
							},
						},
					},
					// one target, otherwise ignored, can't find service
					{
						Host: "waldo.fred.com",
						IngressRuleValue: extensions.IngressRuleValue{
							HTTP: &extensions.HTTPIngressRuleValue{
								Paths: []extensions.HTTPIngressPath{
									{
										Backend: extensions.IngressBackend{
											ServiceName: "waldo",
											ServicePort: intstr.FromInt(9090),
										},
									},
								},
							},
						},
					},
					// should not lead to nil pointer panic in controller code
					{
						Host: "maybe.invalid",
					},
				},
			},
		},

		// supports multiple ingresses
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "armor-2",
				Name:      "qux",
			},
			Spec: extensions.IngressSpec{
				Rules: []extensions.IngressRule{
					{
						Host: "qux.quux.com",
						IngressRuleValue: extensions.IngressRuleValue{
							HTTP: &extensions.HTTPIngressRuleValue{
								Paths: []extensions.HTTPIngressPath{
									{
										Backend: extensions.IngressBackend{
											ServiceName: "qux",
											ServicePort: intstr.FromInt(443),
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	services := []v1.Service{
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "armor",
				Name:      "bar",
			},
			Spec: v1.ServiceSpec{
				ClusterIP: "8.8.8.8",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "armor",
				Name:      "baz",
			},
			Spec: v1.ServiceSpec{
				ClusterIP: "8.8.4.4",
			},
		},
		// different namespace
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "armor-2",
				Name:      "qux",
			},
			Spec: v1.ServiceSpec{
				ClusterIP: "1.2.3.4",
			},
		},
	}

	for _, service := range services {
		_, err := suite.client.Core().Services(service.Namespace).Create(&service)
		suite.Require().NoError(err)
	}

	config, err := suite.controller.GenerateConfig(fixtures)
	suite.Require().NoError(err)

	// TODO(linki): omit waldo entirely?
	suite.Require().Len(config.Hosts, 3)

	// test foo

	suite.Require().Contains(config.Hosts, "foo.bar.com")
	foo := config.Hosts["foo.bar.com"]
	suite.Require().Len(foo.Paths, 2)

	suite.Require().Contains(foo.Paths, "/")
	path := foo.Paths["/"]
	suite.Require().Len(path.Plugins, 1)
	proxy := path.Plugins[0]
	suite.Assert().Equal("proxy", proxy["name"])
	targets := proxy["targets"].([]map[string]string)
	suite.Require().Len(targets, 2)
	suite.Assert().Equal("http://8.8.8.8:80", targets[0]["url"])
	suite.Assert().Equal("http://8.8.4.4:8080", targets[1]["url"])

	// test foo - barbara

	suite.Require().Contains(foo.Paths, "/barbara")
	path = foo.Paths["/barbara"]
	suite.Require().Len(path.Plugins, 1)
	proxy = path.Plugins[0]
	suite.Assert().Equal("proxy", proxy["name"])
	targets = proxy["targets"].([]map[string]string)
	suite.Require().Len(targets, 1)
	suite.Assert().Equal("http://4.3.2.1:9090", targets[0]["url"])

	// test waldo

	// TODO(linki): omit the hostname entirely?
	suite.Require().Contains(config.Hosts, "waldo.fred.com")
	waldo := config.Hosts["waldo.fred.com"]
	suite.Require().Len(waldo.Paths, 1)
	suite.Require().Contains(waldo.Paths, "/")
	path = waldo.Paths["/"]
	suite.Require().Len(path.Plugins, 1)
	proxy = path.Plugins[0]
	suite.Assert().Equal("proxy", proxy["name"])
	targets = proxy["targets"].([]map[string]string)
	suite.Require().Len(targets, 0)

	// test qux

	suite.Require().Contains(config.Hosts, "qux.quux.com")
	qux := config.Hosts["qux.quux.com"]
	suite.Require().Len(qux.Paths, 1)
	suite.Require().Contains(qux.Paths, "/")
	path = qux.Paths["/"]
	suite.Require().Len(path.Plugins, 1)
	proxy = path.Plugins[0]
	suite.Assert().Equal("proxy", proxy["name"])
	targets = proxy["targets"].([]map[string]string)
	suite.Require().Len(targets, 1)
	suite.Assert().Equal("http://1.2.3.4:443", targets[0]["url"])
}

func (suite *Suite) TestIncludeGlobalPlugins() {
	config, err := suite.controller.GenerateConfig(nil)
	suite.Require().NoError(err)

	plugins := []string{}
	for _, plugin := range config.Plugins {
		plugins = append(plugins, plugin["name"].(string))
	}

	suite.Assert().Len(plugins, 2)

	suite.Assert().Contains(plugins, "logger")
	suite.Assert().Contains(plugins, "https-redirect")
}

func (suite *Suite) TestSetupAutoTLS() {
	config, err := suite.controller.GenerateConfig(nil)
	suite.Require().NoError(err)

	suite.Require().NotNil(config.TLS)

	suite.Assert().True(config.TLS.Auto)
	suite.Assert().Equal(":443", config.TLS.Address)
	suite.Assert().Equal("/var/cache/armor", config.TLS.CacheDir)
}

func (suite *Suite) TestEnsureConfigMap() {
	err := suite.controller.EnsureConfigMap(testNamespace, "foo")
	suite.Require().NoError(err)

	_, err = suite.client.Core().ConfigMaps(testNamespace).Get("foo", metav1.GetOptions{})
	suite.Require().NoError(err)

	err = suite.controller.EnsureConfigMap(testNamespace, "foo")
	suite.Require().NoError(err)
}

func (suite *Suite) TestWriteConfigToConfigMapByName() {
	configMap := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      "foo",
		},
	}

	_, err := suite.client.Core().ConfigMaps(testNamespace).Create(configMap)
	suite.Require().NoError(err)

	config := &armor.Armor{
		Hosts: map[string]*armor.Host{
			"foo": {
				CertFile: "cert",
			},
		},
	}

	err = suite.controller.WriteConfigToConfigMapByName(config, testNamespace, "foo", "bar")
	suite.Require().NoError(err)

	configMap, err = suite.client.Core().ConfigMaps(testNamespace).Get("foo", metav1.GetOptions{})
	suite.Require().NoError(err)

	configMapAnnotation := configMap.Annotations["armor.labstack.com/configHash"]

	suite.Assert().Equal(getConfigHash(config), configMapAnnotation)

	var loaded armor.Armor

	err = yaml.Unmarshal([]byte(configMap.Data["bar"]), &loaded)
	suite.Require().NoError(err)

	suite.Require().Len(loaded.Hosts, 1)
	suite.Require().NotNil(loaded.Hosts["foo"])
	suite.Assert().Equal("cert", loaded.Hosts["foo"].CertFile)
}

func (suite *Suite) TestWriteConfigToConfigMap() {
	configMap := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      "foo",
		},
		Data: map[string]string{
			"bar": "baz",
		},
	}

	_, err := suite.client.Core().ConfigMaps(testNamespace).Create(configMap)
	suite.Require().NoError(err)

	config := &armor.Armor{
		Hosts: map[string]*armor.Host{
			"foo": {
				CertFile: "cert",
			},
		},
	}

	err = suite.controller.WriteConfigToConfigMap(config, configMap, "bar")
	suite.Require().NoError(err)

	configMap, err = suite.client.Core().ConfigMaps(testNamespace).Get("foo", metav1.GetOptions{})
	suite.Require().NoError(err)

	var loaded armor.Armor

	err = yaml.Unmarshal([]byte(configMap.Data["bar"]), &loaded)
	suite.Require().NoError(err)

	suite.Require().Len(loaded.Hosts, 1)
	suite.Require().NotNil(loaded.Hosts["foo"])
	suite.Assert().Equal("cert", loaded.Hosts["foo"].CertFile)
}

func (suite *Suite) TestWriteConfigToWriter() {
	config := &armor.Armor{
		Hosts: map[string]*armor.Host{
			"foo": {
				CertFile: "cert",
			},
		},
	}

	var buffer bytes.Buffer

	err := suite.controller.WriteConfigToWriter(config, &buffer)
	suite.Require().NoError(err)

	var loaded armor.Armor

	err = yaml.Unmarshal(buffer.Bytes(), &loaded)
	suite.Require().NoError(err)

	suite.Require().Len(loaded.Hosts, 1)
	suite.Require().NotNil(loaded.Hosts["foo"])
	suite.Assert().Equal("cert", loaded.Hosts["foo"].CertFile)
}

func (suite *Suite) TestUpdateDeploymentByName() {
	deployment := &extensions.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      "foo",
		},
	}

	_, err := suite.client.Extensions().Deployments(testNamespace).Create(deployment)
	suite.Require().NoError(err)

	config := &armor.Armor{}

	err = suite.controller.UpdateDeploymentByName(testNamespace, "foo", config)
	suite.Require().NoError(err)

	deployment, err = suite.client.Extensions().Deployments(testNamespace).Get("foo", metav1.GetOptions{})
	suite.Require().NoError(err)

	deploymentAnnotation := deployment.Annotations["armor.labstack.com/configHash"]
	suite.Assert().Equal(getConfigHash(config), deploymentAnnotation)

	podAnnotation := deployment.Spec.Template.Annotations["armor.labstack.com/configHash"]
	suite.Assert().Equal(getConfigHash(config), podAnnotation)
}

func (suite *Suite) TestUpdateDeployment() {
	deployment := &extensions.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      "foo",
		},
	}

	_, err := suite.client.Extensions().Deployments(testNamespace).Create(deployment)
	suite.Require().NoError(err)

	config := &armor.Armor{}

	err = suite.controller.UpdateDeployment(deployment, config)
	suite.Require().NoError(err)

	deployment, err = suite.client.Extensions().Deployments(testNamespace).Get("foo", metav1.GetOptions{})
	suite.Require().NoError(err)

	deploymentAnnotation := deployment.Annotations["armor.labstack.com/configHash"]
	suite.Assert().Equal(getConfigHash(config), deploymentAnnotation)

	podAnnotation := deployment.Spec.Template.Annotations["armor.labstack.com/configHash"]
	suite.Assert().Equal(getConfigHash(config), podAnnotation)
}

func (suite *Suite) TestUpdateDaemonSetByName() {
	daemonSet := &extensions.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      "foo",
		},
	}

	_, err := suite.client.Extensions().DaemonSets(testNamespace).Create(daemonSet)
	suite.Require().NoError(err)

	config := &armor.Armor{}

	daemonSet, err = suite.controller.UpdateDaemonSetByName(testNamespace, "foo", config)
	suite.Require().NoError(err)

	daemonSet, err = suite.client.Extensions().DaemonSets(testNamespace).Get(daemonSet.Name, metav1.GetOptions{})
	suite.Require().NoError(err)

	daemonSetAnnotation := daemonSet.Annotations["armor.labstack.com/configHash"]
	suite.Assert().Equal(getConfigHash(config), daemonSetAnnotation)

	podAnnotation := daemonSet.Spec.Template.Annotations["armor.labstack.com/configHash"]
	suite.Assert().Equal(getConfigHash(config), podAnnotation)
}

func (suite *Suite) TestUpdateDaemonSet() {
	daemonSet := &extensions.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      "foo",
		},
	}

	_, err := suite.client.Extensions().DaemonSets(testNamespace).Create(daemonSet)
	suite.Require().NoError(err)

	config := &armor.Armor{}

	daemonSet, err = suite.controller.UpdateDaemonSet(daemonSet, config)
	suite.Require().NoError(err)

	daemonSet, err = suite.client.Extensions().DaemonSets(testNamespace).Get(daemonSet.Name, metav1.GetOptions{})
	suite.Require().NoError(err)

	daemonSetAnnotation := daemonSet.Annotations["armor.labstack.com/configHash"]
	suite.Assert().Equal(getConfigHash(config), daemonSetAnnotation)

	podAnnotation := daemonSet.Spec.Template.Annotations["armor.labstack.com/configHash"]
	suite.Assert().Equal(getConfigHash(config), podAnnotation)
}

func (suite *Suite) TestGetNodeIPs() {
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "foo",
		},
		Status: v1.NodeStatus{
			Addresses: []v1.NodeAddress{
				{
					Type:    v1.NodeExternalIP,
					Address: "54.10.11.12",
				},
				{
					Type:    v1.NodeInternalIP,
					Address: "10.0.1.1",
				},
			},
		},
	}

	_, err := suite.client.Core().Nodes().Create(node)
	suite.Require().NoError(err)

	nodeIPs, err := suite.controller.GetNodeIPs()
	suite.Require().NoError(err)

	suite.Assert().Equal([]string{"54.10.11.12"}, nodeIPs)
}

func (suite *Suite) TestGetNodeNamesByPodLabelSelector() {
	nodes := []*v1.Node{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "foo",
			},
			Status: v1.NodeStatus{
				Addresses: []v1.NodeAddress{
					{
						Type:    v1.NodeExternalIP,
						Address: "8.8.8.8",
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "bar",
			},
			Status: v1.NodeStatus{
				Addresses: []v1.NodeAddress{
					{
						Type:    v1.NodeExternalIP,
						Address: "8.8.4.4",
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "baz",
			},
			Status: v1.NodeStatus{
				Addresses: []v1.NodeAddress{
					{
						Type:    v1.NodeExternalIP,
						Address: "1.2.3.4",
					},
				},
			},
		},
	}

	labelSet := labels.Set{
		"app": "armor",
	}

	pods := []*v1.Pod{
		// should be included
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: testNamespace,
				Name:      "foo",
				Labels:    labelSet,
			},
			Spec: v1.PodSpec{
				NodeName: nodes[0].Name,
			},
		},

		// should not be included: different namespace
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "armor",
				Name:      "bar",
				Labels:    labelSet,
			},
			Spec: v1.PodSpec{
				NodeName: nodes[1].Name,
			},
		},

		// should not be included: doesn't match labels
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: testNamespace,
				Name:      "qux",
			},
			Spec: v1.PodSpec{
				NodeName: nodes[2].Name,
			},
		},

		// should not be included: duplicate
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: testNamespace,
				Name:      "waldo",
				Labels:    labelSet,
			},
			Spec: v1.PodSpec{
				NodeName: nodes[0].Name,
			},
		},

		// should not be included: no assigned node
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: testNamespace,
				Name:      "quux",
				Labels:    labelSet,
			},
		},
	}

	for _, node := range nodes {
		_, err := suite.client.Core().Nodes().Create(node)
		suite.Require().NoError(err)
	}

	for _, pod := range pods {
		_, err := suite.client.Core().Pods(pod.Namespace).Create(pod)
		suite.Require().NoError(err)
	}

	nodeNames, err := suite.controller.GetNodeNamesByPodLabelSelector(testNamespace, labels.SelectorFromSet(labelSet))
	suite.Require().NoError(err)

	suite.Assert().Equal([]string{"foo"}, nodeNames)
}

func (suite *Suite) TestGetExternalNodeIPsByNodeNames() {
	// should match but only external IP
	node1 := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "foo",
		},
		Status: v1.NodeStatus{
			Addresses: []v1.NodeAddress{
				{
					Type:    v1.NodeExternalIP,
					Address: "54.10.11.12",
				},
				{
					Type:    v1.NodeInternalIP,
					Address: "10.0.1.1",
				},
			},
		},
	}

	// should not match because name is not in list
	node2 := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "bar",
		},
		Status: v1.NodeStatus{
			Addresses: []v1.NodeAddress{
				{
					Type:    v1.NodeExternalIP,
					Address: "1.2.3.4",
				},
			},
		},
	}

	_, err := suite.client.Core().Nodes().Create(node1)
	suite.Require().NoError(err)

	_, err = suite.client.Core().Nodes().Create(node2)
	suite.Require().NoError(err)

	nodeIPs, err := suite.controller.GetExternalNodeIPsByNodeNames([]string{"foo", "qux"})
	suite.Require().NoError(err)

	suite.Assert().Equal([]string{"54.10.11.12"}, nodeIPs)
}

func (suite *Suite) TestGetConfigHash() {
	config := &armor.Armor{
		Hosts: map[string]*armor.Host{
			"foo": {
				CertFile: "cert",
			},
		},
	}
	configHash := getConfigHash(config)

	otherConfig := &armor.Armor{
		Hosts: map[string]*armor.Host{
			"foo": {
				CertFile: "other-cert",
			},
		},
	}

	suite.Assert().Equal(configHash, getConfigHash(config))
	suite.Assert().NotEqual(configHash, getConfigHash(otherConfig))
}

func (suite *Suite) TestUpstreamServiceURL() {
	for _, test := range []struct {
		ip, port, want string
	}{
		{"8.8.8.8", "80", "http://8.8.8.8:80"},
		{"1.2.3.4", "8080", "http://1.2.3.4:8080"},
	} {
		got := upstreamServiceURL(test.ip, test.port)
		suite.Assert().Equal(test.want, got)
	}
}

// TODO(linki): make tests independent of ordering
func assertLoadBalancerIPs(t *testing.T, ingress *extensions.Ingress, expected []string) {
	loadBalancers := ingress.Status.LoadBalancer.Ingress

	require.Len(t, loadBalancers, len(expected))

	for i := range loadBalancers {
		assert.Equal(t, expected[i], loadBalancers[i].IP)
	}
}
