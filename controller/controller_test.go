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
)

const (
	testNamespace = "armor-test"
)

func TestNew(t *testing.T) {
	client := fake.NewSimpleClientset()
	controller := NewController(client)

	assert.Equal(t, client, controller.Client)
}

func TestGetIngresses(t *testing.T) {
	controller, _ := newTestController(t, []*extensions.Ingress{
		// main object under test
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "foo",
				Namespace: testNamespace,
				Annotations: map[string]string{
					"kubernetes.io/ingress.class": "armor",
				},
			},
		},
		// test that it supports multiple ingresses
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "bar",
				Namespace: testNamespace,
				Annotations: map[string]string{
					"kubernetes.io/ingress.class": "armor",
				},
			},
		},
		// should be filtered out by annotation
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "baz",
				Namespace: testNamespace,
			},
		},
	})

	ingresses, err := controller.GetIngresses()
	assert.NoError(t, err)

	assertIngresses(t, ingresses, []map[string]string{
		{"name": "foo", "namespace": testNamespace},
		{"name": "bar", "namespace": testNamespace},
	})
}

func TestUpdateIngressLoadBalancer(t *testing.T) {
	foo := &extensions.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      "foo",
		},
	}

	bar := &extensions.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      "bar",
		},
	}

	for _, ti := range []struct {
		title         string
		ingresses     []*extensions.Ingress
		loadBalancers []string
	}{
		{
			title:         "it updates multiple ingresses with multiple IPs",
			ingresses:     []*extensions.Ingress{foo, bar},
			loadBalancers: []string{"8.8.8.8", "8.8.4.4"},
		},
	} {
		t.Run(ti.title, func(t *testing.T) {
			controller, client := newTestController(t, ti.ingresses)

			err := controller.UpdateIngressLoadBalancers(ti.ingresses, ti.loadBalancers)
			assert.NoError(t, err)

			for _, ing := range ti.ingresses {
				ingress, err := client.Extensions().Ingresses(ing.Namespace).Get(ing.Name, metav1.GetOptions{})
				require.NoError(t, err)

				assertLoadBalancerIPs(t, ingress, ti.loadBalancers)
			}
		})
	}
}

func TestGenerateConfig(t *testing.T) {
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

	client := fake.NewSimpleClientset()

	for _, service := range services {
		_, err := client.Core().Services(service.Namespace).Create(&service)
		require.NoError(t, err)
	}

	controller := NewController(client)

	config, err := controller.GenerateConfig(fixtures)
	require.NoError(t, err)

	// TODO(linki): omit waldo entirely?
	require.Len(t, config.Hosts, 3)

	// test foo

	require.Contains(t, config.Hosts, "foo.bar.com")
	foo := config.Hosts["foo.bar.com"]
	require.Len(t, foo.Plugins, 1)
	proxy := foo.Plugins[0]
	assert.Equal(t, "proxy", proxy["name"])
	targets := proxy["targets"].([]map[string]string)
	require.Len(t, targets, 2)
	assert.Equal(t, "http://8.8.8.8:80", targets[0]["url"])
	assert.Equal(t, "http://8.8.4.4:8080", targets[1]["url"])

	// test waldo

	// TODO(linki): omit the hostname entirely?
	require.Contains(t, config.Hosts, "waldo.fred.com")
	waldo := config.Hosts["waldo.fred.com"]
	require.Len(t, waldo.Plugins, 1)
	proxy = waldo.Plugins[0]
	assert.Equal(t, "proxy", proxy["name"])
	targets = proxy["targets"].([]map[string]string)
	require.Len(t, targets, 0)

	// test qux

	require.Contains(t, config.Hosts, "qux.quux.com")
	qux := config.Hosts["qux.quux.com"]
	require.Len(t, qux.Plugins, 1)
	proxy = qux.Plugins[0]
	assert.Equal(t, "proxy", proxy["name"])
	targets = proxy["targets"].([]map[string]string)
	require.Len(t, targets, 1)
	assert.Equal(t, "http://1.2.3.4:443", targets[0]["url"])
}

func TestIncludeGlobalPlugins(t *testing.T) {
	controller, _ := newTestController(t, nil)

	config, err := controller.GenerateConfig(nil)
	require.NoError(t, err)

	plugins := []string{}
	for _, plugin := range config.Plugins {
		plugins = append(plugins, plugin["name"].(string))
	}

	assert.Len(t, plugins, 2)

	assert.Contains(t, plugins, "logger")
	assert.Contains(t, plugins, "https-redirect")
}

func TestSetupAutoTLS(t *testing.T) {
	controller, _ := newTestController(t, nil)

	config, err := controller.GenerateConfig(nil)
	require.NoError(t, err)

	require.NotNil(t, config.TLS)

	assert.True(t, config.TLS.Auto)
	assert.Equal(t, ":443", config.TLS.Address)
	assert.Equal(t, "/var/cache/armor", config.TLS.CacheDir)
}

func TestEnsureConfigMap(t *testing.T) {
	controller, client := newTestController(t, nil)

	err := controller.EnsureConfigMap(testNamespace, "foo")
	require.NoError(t, err)

	_, err = client.Core().ConfigMaps(testNamespace).Get("foo", metav1.GetOptions{})
	require.NoError(t, err)

	err = controller.EnsureConfigMap(testNamespace, "foo")
	require.NoError(t, err)
}

func TestWriteConfigToConfigMapByName(t *testing.T) {
	configMap := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      "foo",
		},
	}

	client := fake.NewSimpleClientset()

	_, err := client.Core().ConfigMaps(testNamespace).Create(configMap)
	require.NoError(t, err)

	config := &armor.Armor{
		Hosts: map[string]*armor.Host{
			"foo": {
				CertFile: "cert",
			},
		},
	}

	controller := NewController(client)

	err = controller.WriteConfigToConfigMapByName(config, testNamespace, "foo", "bar")
	require.NoError(t, err)

	configMap, err = client.Core().ConfigMaps(testNamespace).Get("foo", metav1.GetOptions{})
	require.NoError(t, err)

	configMapAnnotation := configMap.Annotations["armor.labstack.com/configHash"]

	assert.Equal(t, getConfigHash(config), configMapAnnotation)

	var loaded armor.Armor

	err = yaml.Unmarshal([]byte(configMap.Data["bar"]), &loaded)
	require.NoError(t, err)

	require.Len(t, loaded.Hosts, 1)
	require.NotNil(t, loaded.Hosts["foo"])
	assert.Equal(t, "cert", loaded.Hosts["foo"].CertFile)
}

func TestWriteConfigToConfigMap(t *testing.T) {
	configMap := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      "foo",
		},
		Data: map[string]string{
			"bar": "baz",
		},
	}

	client := fake.NewSimpleClientset()

	_, err := client.Core().ConfigMaps(testNamespace).Create(configMap)
	require.NoError(t, err)

	config := &armor.Armor{
		Hosts: map[string]*armor.Host{
			"foo": {
				CertFile: "cert",
			},
		},
	}

	controller := NewController(client)

	err = controller.WriteConfigToConfigMap(config, configMap, "bar")
	require.NoError(t, err)

	configMap, err = client.Core().ConfigMaps(testNamespace).Get("foo", metav1.GetOptions{})
	require.NoError(t, err)

	var loaded armor.Armor

	err = yaml.Unmarshal([]byte(configMap.Data["bar"]), &loaded)
	require.NoError(t, err)

	require.Len(t, loaded.Hosts, 1)
	require.NotNil(t, loaded.Hosts["foo"])
	assert.Equal(t, "cert", loaded.Hosts["foo"].CertFile)
}

func TestWriteConfigToWriter(t *testing.T) {
	config := &armor.Armor{
		Hosts: map[string]*armor.Host{
			"foo": {
				CertFile: "cert",
			},
		},
	}

	controller := NewController(fake.NewSimpleClientset())

	var buffer bytes.Buffer

	err := controller.WriteConfigToWriter(config, &buffer)
	require.NoError(t, err)

	var loaded armor.Armor

	err = yaml.Unmarshal(buffer.Bytes(), &loaded)
	require.NoError(t, err)

	require.Len(t, loaded.Hosts, 1)
	require.NotNil(t, loaded.Hosts["foo"])
	assert.Equal(t, "cert", loaded.Hosts["foo"].CertFile)
}

func TestUpdateDeploymentByName(t *testing.T) {
	deployment := &extensions.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      "foo",
		},
	}

	client := fake.NewSimpleClientset()

	_, err := client.Extensions().Deployments(testNamespace).Create(deployment)
	require.NoError(t, err)

	controller := NewController(client)
	config := &armor.Armor{}

	err = controller.UpdateDeploymentByName(testNamespace, "foo", config)
	require.NoError(t, err)

	deployment, err = client.Extensions().Deployments(testNamespace).Get("foo", metav1.GetOptions{})
	require.NoError(t, err)

	deploymentAnnotation := deployment.Annotations["armor.labstack.com/configHash"]
	assert.Equal(t, getConfigHash(config), deploymentAnnotation)

	podAnnotation := deployment.Spec.Template.Annotations["armor.labstack.com/configHash"]
	assert.Equal(t, getConfigHash(config), podAnnotation)
}

func TestUpdateDeployment(t *testing.T) {
	deployment := &extensions.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      "foo",
		},
	}

	client := fake.NewSimpleClientset()

	_, err := client.Extensions().Deployments(testNamespace).Create(deployment)
	require.NoError(t, err)

	controller := NewController(client)
	config := &armor.Armor{}

	err = controller.UpdateDeployment(deployment, config)
	require.NoError(t, err)

	deployment, err = client.Extensions().Deployments(testNamespace).Get("foo", metav1.GetOptions{})
	require.NoError(t, err)

	deploymentAnnotation := deployment.Annotations["armor.labstack.com/configHash"]
	assert.Equal(t, getConfigHash(config), deploymentAnnotation)

	podAnnotation := deployment.Spec.Template.Annotations["armor.labstack.com/configHash"]
	assert.Equal(t, getConfigHash(config), podAnnotation)
}

func TestUpdateDaemonSetByName(t *testing.T) {
	daemonSet := &extensions.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      "foo",
		},
	}

	client := fake.NewSimpleClientset()

	_, err := client.Extensions().DaemonSets(testNamespace).Create(daemonSet)
	require.NoError(t, err)

	controller := NewController(client)
	config := &armor.Armor{}

	daemonSet, err = controller.UpdateDaemonSetByName(testNamespace, "foo", config)
	require.NoError(t, err)

	daemonSet, err = client.Extensions().DaemonSets(testNamespace).Get(daemonSet.Name, metav1.GetOptions{})
	require.NoError(t, err)

	daemonSetAnnotation := daemonSet.Annotations["armor.labstack.com/configHash"]
	assert.Equal(t, getConfigHash(config), daemonSetAnnotation)

	podAnnotation := daemonSet.Spec.Template.Annotations["armor.labstack.com/configHash"]
	assert.Equal(t, getConfigHash(config), podAnnotation)
}

func TestUpdateDaemonSet(t *testing.T) {
	daemonSet := &extensions.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      "foo",
		},
	}

	client := fake.NewSimpleClientset()

	_, err := client.Extensions().DaemonSets(testNamespace).Create(daemonSet)
	require.NoError(t, err)

	controller := NewController(client)
	config := &armor.Armor{}

	daemonSet, err = controller.UpdateDaemonSet(daemonSet, config)
	require.NoError(t, err)

	daemonSet, err = client.Extensions().DaemonSets(testNamespace).Get(daemonSet.Name, metav1.GetOptions{})
	require.NoError(t, err)

	daemonSetAnnotation := daemonSet.Annotations["armor.labstack.com/configHash"]
	assert.Equal(t, getConfigHash(config), daemonSetAnnotation)

	podAnnotation := daemonSet.Spec.Template.Annotations["armor.labstack.com/configHash"]
	assert.Equal(t, getConfigHash(config), podAnnotation)
}

func TestGetNodeIPs(t *testing.T) {
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

	client := fake.NewSimpleClientset()

	_, err := client.Core().Nodes().Create(node)
	require.NoError(t, err)

	controller := NewController(client)

	nodeIPs, err := controller.GetNodeIPs()
	require.NoError(t, err)

	assert.Equal(t, []string{"54.10.11.12"}, nodeIPs)
}

func TestGetNodeNamesByPodLabelSelector(t *testing.T) {
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

	client := fake.NewSimpleClientset()

	for _, node := range nodes {
		_, err := client.Core().Nodes().Create(node)
		require.NoError(t, err)
	}

	for _, pod := range pods {
		_, err := client.Core().Pods(pod.Namespace).Create(pod)
		require.NoError(t, err)
	}

	controller := NewController(client)

	nodeNames, err := controller.GetNodeNamesByPodLabelSelector(testNamespace, labels.SelectorFromSet(labelSet))
	require.NoError(t, err)

	assert.Equal(t, []string{"foo"}, nodeNames)
}

func TestGetExternalNodeIPsByNodeNames(t *testing.T) {
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

	client := fake.NewSimpleClientset()

	_, err := client.Core().Nodes().Create(node1)
	require.NoError(t, err)

	_, err = client.Core().Nodes().Create(node2)
	require.NoError(t, err)

	controller := NewController(client)

	nodeIPs, err := controller.GetExternalNodeIPsByNodeNames([]string{"foo", "qux"})
	require.NoError(t, err)

	assert.Equal(t, []string{"54.10.11.12"}, nodeIPs)
}

func TestGetConfigHash(t *testing.T) {
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

	assert.Equal(t, configHash, getConfigHash(config))
	assert.NotEqual(t, configHash, getConfigHash(otherConfig))
}

func TestUpstreamServiceURL(t *testing.T) {
	for _, test := range []struct {
		ip, port, want string
	}{
		{"8.8.8.8", "80", "http://8.8.8.8:80"},
		{"1.2.3.4", "8080", "http://1.2.3.4:8080"},
	} {
		got := upstreamServiceURL(test.ip, test.port)
		assert.Equal(t, test.want, got)
	}
}

func newTestController(t *testing.T, ingresses []*extensions.Ingress) (*Controller, kubernetes.Interface) {
	client := fake.NewSimpleClientset()
	controller := NewController(client)
	seedIngresses(t, client, ingresses)
	return controller, client
}

func seedIngresses(t *testing.T, client kubernetes.Interface, ingresses []*extensions.Ingress) {
	for _, ingress := range ingresses {
		_, err := client.Extensions().Ingresses(testNamespace).Create(ingress)
		require.NoError(t, err)
	}
}

// TODO(linki): make tests independent of ordering
func assertIngresses(t *testing.T, ingresses []*extensions.Ingress, expected []map[string]string) {
	require.Len(t, ingresses, len(expected))

	for i := range ingresses {
		assertIngress(t, ingresses[i], expected[i])
	}
}

func assertIngress(t *testing.T, ingress *extensions.Ingress, expected map[string]string) {
	assert.Equal(t, expected["name"], ingress.Name)
	assert.Equal(t, expected["namespace"], ingress.Namespace)
}

// TODO(linki): make tests independent of ordering
func assertLoadBalancerIPs(t *testing.T, ingress *extensions.Ingress, expected []string) {
	loadBalancers := ingress.Status.LoadBalancer.Ingress

	require.Len(t, loadBalancers, len(expected))

	for i := range loadBalancers {
		assert.Equal(t, expected[i], loadBalancers[i].IP)
	}
}
