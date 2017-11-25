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
	"k8s.io/client-go/kubernetes/fake"
)

func TestNew(t *testing.T) {
	client := fake.NewSimpleClientset()
	controller := NewController(client)

	if controller.Client != client {
		t.Errorf("expected %#v, got %#v", client, controller.Client)
	}
}

func TestGetIngresses(t *testing.T) {
	fixtures := []*extensions.Ingress{
		// main object under test
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "armor-test",
				Name:      "foo",
				Annotations: map[string]string{
					"kubernetes.io/ingress.class": "armor",
				},
			},
		},

		// supports multiple ingresses
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "armor-test",
				Name:      "bar",
				Annotations: map[string]string{
					"kubernetes.io/ingress.class": "armor",
				},
			},
		},

		// filtered out by annotation
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "armor-test",
				Name:      "baz",
			},
		},
	}

	client := fake.NewSimpleClientset()

	for _, fixture := range fixtures {
		if _, err := client.Extensions().Ingresses("armor-test").Create(fixture); err != nil {
			t.Fatal(err)
		}
	}

	controller := NewController(client)

	ingresses, err := controller.GetIngresses()
	if err != nil {
		t.Error(err)
	}

	if len(ingresses) != 2 {
		t.Fatalf("expected 2, got %d", len(ingresses))
	}

	// TODO(linki): make tests independent of ordering
	ingress := ingresses[0]

	if ingress.Name != "foo" {
		t.Errorf("expected foo, got %s", ingress.Name)
	}

	if ingress.Namespace != "armor-test" {
		t.Errorf("expected default, got %s", ingress.Namespace)
	}

	ingress = ingresses[1]

	if ingress.Name != "bar" {
		t.Errorf("expected bar, got %s", ingress.Name)
	}

	if ingress.Namespace != "armor-test" {
		t.Errorf("expected armor, got %s", ingress.Namespace)
	}
}

func TestUpdateIngressLoadBalancer(t *testing.T) {
	fixtures := []extensions.Ingress{
		// main object under test
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "armor-test",
				Name:      "foo",
			},
		},

		// supports multiple ingresses
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "armor-test",
				Name:      "bar",
			},
		},
	}

	client := fake.NewSimpleClientset()

	for _, fixture := range fixtures {
		if _, err := client.Extensions().Ingresses("armor-test").Create(&fixture); err != nil {
			t.Fatal(err)
		}
	}

	controller := NewController(client)

	if err := controller.UpdateIngressLoadBalancers(fixtures, "8.8.8.8", "8.8.4.4"); err != nil {
		t.Error(err)
	}

	// check first one

	ingress, err := client.Extensions().Ingresses("armor-test").Get("foo", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}

	loadBalancerIPs := ingress.Status.LoadBalancer.Ingress

	if len(loadBalancerIPs) != 2 {
		t.Fatalf("expected 2, got %d", len(loadBalancerIPs))
	}

	// TODO(linki): make tests independent of ordering
	loadBalancerIP := loadBalancerIPs[0]

	if loadBalancerIP.IP != "8.8.8.8" {
		t.Errorf("expected 8.8.8.8, got %s", loadBalancerIP.IP)
	}

	loadBalancerIP = loadBalancerIPs[1]

	if loadBalancerIP.IP != "8.8.4.4" {
		t.Errorf("expected 8.8.4.4, got %s", loadBalancerIP.IP)
	}

	// check second one

	ingress, err = client.Extensions().Ingresses("armor-test").Get("bar", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}

	loadBalancerIPs = ingress.Status.LoadBalancer.Ingress

	if len(loadBalancerIPs) != 2 {
		t.Fatalf("expected 2, got %d", len(loadBalancerIPs))
	}

	// TODO(linki): make tests independent of ordering
	loadBalancerIP = loadBalancerIPs[0]

	if loadBalancerIP.IP != "8.8.8.8" {
		t.Errorf("expected 8.8.8.8, got %s", loadBalancerIP.IP)
	}

	loadBalancerIP = loadBalancerIPs[1]

	if loadBalancerIP.IP != "8.8.4.4" {
		t.Errorf("expected 8.8.4.4, got %s", loadBalancerIP.IP)
	}
}

func TestGenerateConfig(t *testing.T) {
	fixtures := []extensions.Ingress{
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
		if _, err := client.Core().Services(service.Namespace).Create(&service); err != nil {
			t.Fatal(err)
		}
	}

	controller := NewController(client)

	config, err := controller.GenerateConfig(fixtures...)
	if err != nil {
		t.Fatal(err)
	}

	if len(config.Hosts) != 3 {
		t.Fatalf("expected 3, got %d", len(config.Hosts))
	}

	if _, exists := config.Hosts["foo.bar.com"]; !exists {
		t.Errorf("expected something, got nothing")
	}

	if _, exists := config.Hosts["waldo.fred.com"]; !exists {
		t.Errorf("expected something, got nothing")
	}

	if _, exists := config.Hosts["qux.quux.com"]; !exists {
		t.Errorf("expected something, got nothing")
	}

	// test foo

	foo := config.Hosts["foo.bar.com"]

	if len(foo.Plugins) != 1 {
		t.Fatalf("expected 1, got %d", len(foo.Plugins))
	}

	proxy := foo.Plugins[0]

	if _, exists := proxy["name"]; !exists {
		t.Fatalf("expected something, got nothing")
	}

	if _, exists := proxy["targets"]; !exists {
		t.Fatalf("expected something, got nothing")
	}

	if proxy["name"].(string) != "proxy" {
		t.Fatalf("expected proxy, got %#v", proxy["name"])
	}

	targets := proxy["targets"].([]map[string]string)

	if len(targets) != 2 {
		t.Fatalf("expected 2, got %d", len(targets))
	}

	expected := "http://8.8.8.8:80"

	// TODO(linki): make tests independent of ordering
	if targets[0]["url"] != expected {
		t.Fatalf("expected %s, got %#v", expected, targets[0]["url"])
	}

	expected = "http://8.8.4.4:8080"

	if targets[1]["url"] != expected {
		t.Fatalf("expected %s, got %#v", expected, targets[0]["url"])
	}

	// TODO(linki): test waldo

	// test qux

	qux := config.Hosts["qux.quux.com"]

	if len(qux.Plugins) != 1 {
		t.Fatalf("expected 1, got %d", len(qux.Plugins))
	}

	proxy = qux.Plugins[0]

	if _, exists := proxy["name"]; !exists {
		t.Fatalf("expected something, got nothing")
	}

	if _, exists := proxy["targets"]; !exists {
		t.Fatalf("expected something, got nothing")
	}

	if proxy["name"].(string) != "proxy" {
		t.Fatalf("expected proxy, got %#v", proxy["name"])
	}

	targets = proxy["targets"].([]map[string]string)

	if len(targets) != 1 {
		t.Fatalf("expected 1, got %d", len(targets))
	}

	expected = "http://1.2.3.4:443"

	if targets[0]["url"] != expected {
		t.Fatalf("expected %s, got %#v", expected, targets[0]["url"])
	}
}

func TestIncludeGlobalPlugins(t *testing.T) {
	controller := NewController(fake.NewSimpleClientset())

	config, err := controller.GenerateConfig(extensions.Ingress{})
	if err != nil {
		t.Fatal(err)
	}

	if len(config.Plugins) != 2 {
		t.Fatalf("expected 2, got %d", len(config.Plugins))
	}

	var tests = []struct {
		name string
	}{
		{"logger"},
		{"https-redirect"},
	}

	for _, test := range tests {
		included := false

		for _, plugin := range config.Plugins {
			if plugin["name"] == test.name {
				included = true
			}
		}

		if !included {
			t.Errorf("plugin %s is missing", test.name)
		}
	}
}

func TestSetupAutoTLS(t *testing.T) {
	controller := NewController(fake.NewSimpleClientset())

	config, err := controller.GenerateConfig(extensions.Ingress{})
	if err != nil {
		t.Fatal(err)
	}

	if config.TLS == nil {
		t.Fatalf("TLS not configured.")
	}

	if config.TLS.Address != ":443" {
		t.Errorf("TLS doesn't listen on :443.")
	}

	if !config.TLS.Auto {
		t.Errorf("auto TLS not configured.")
	}

	if config.TLS.CacheDir != "/var/cache/armor" {
		t.Errorf("expected /var/cache/armor, got %#v", config.TLS.CacheDir)
	}
}

func TestEnsureConfigMap(t *testing.T) {
	client := fake.NewSimpleClientset()
	controller := NewController(client)

	if err := controller.EnsureConfigMap("armor-test", "foo"); err != nil {
		t.Fatal(err)
	}

	if _, err := client.Core().ConfigMaps("armor-test").Get("foo", metav1.GetOptions{}); err != nil {
		t.Fatal(err)
	}

	if err := controller.EnsureConfigMap("armor-test", "foo"); err != nil {
		t.Fatal(err)
	}
}

func TestWriteConfigToConfigMapByName(t *testing.T) {
	configMap := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "armor-test",
			Name:      "foo",
		},
	}

	client := fake.NewSimpleClientset()

	if _, err := client.Core().ConfigMaps("armor-test").Create(configMap); err != nil {
		t.Fatal(err)
	}

	config := &armor.Armor{
		Hosts: map[string]*armor.Host{
			"foo": {
				CertFile: "cert",
			},
		},
	}

	controller := NewController(client)

	if err := controller.WriteConfigToConfigMapByName(config, "armor-test", "foo", "bar"); err != nil {
		t.Fatal(err)
	}

	configMap, err := client.Core().ConfigMaps("armor-test").Get("foo", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}

	configMapAnnotation := configMap.Annotations["armor.labstack.com/configHash"]

	if configMapAnnotation != getConfigHash(config) {
		t.Errorf("expected %#v, got %#v", getConfigHash(config), configMapAnnotation)
	}

	var loaded armor.Armor

	if err := yaml.Unmarshal([]byte(configMap.Data["bar"]), &loaded); err != nil {
		t.Fatal(err)
	}

	if len(loaded.Hosts) != 1 {
		t.Fatalf("expected 1, got %d", len(loaded.Hosts))
	}

	if _, exists := loaded.Hosts["foo"]; !exists {
		t.Errorf("expected something, got nothing")
	}

	if loaded.Hosts["foo"].CertFile != "cert" {
		t.Errorf("expected cert, got %#v", loaded.Hosts["foo"].CertFile)
	}
}

func TestWriteConfigToConfigMap(t *testing.T) {
	configMap := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "armor-test",
			Name:      "foo",
		},
		Data: map[string]string{
			"bar": "baz",
		},
	}

	client := fake.NewSimpleClientset()

	if _, err := client.Core().ConfigMaps("armor-test").Create(configMap); err != nil {
		t.Fatal(err)
	}

	config := &armor.Armor{
		Hosts: map[string]*armor.Host{
			"foo": {
				CertFile: "cert",
			},
		},
	}

	controller := NewController(client)

	if err := controller.WriteConfigToConfigMap(config, configMap, "bar"); err != nil {
		t.Fatal(err)
	}

	configMap, err := client.Core().ConfigMaps("armor-test").Get("foo", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}

	var loaded armor.Armor

	if err := yaml.Unmarshal([]byte(configMap.Data["bar"]), &loaded); err != nil {
		t.Fatal(err)
	}

	if len(loaded.Hosts) != 1 {
		t.Fatalf("expected 1, got %d", len(loaded.Hosts))
	}

	if _, exists := loaded.Hosts["foo"]; !exists {
		t.Errorf("expected something, got nothing")
	}

	if loaded.Hosts["foo"].CertFile != "cert" {
		t.Fatalf("expected cert, got %#v", loaded.Hosts["foo"].CertFile)
	}
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

	if err := controller.WriteConfigToWriter(config, &buffer); err != nil {
		t.Fatal(err)
	}

	var loaded armor.Armor

	if err := yaml.Unmarshal(buffer.Bytes(), &loaded); err != nil {
		t.Fatal(err)
	}

	if len(loaded.Hosts) != 1 {
		t.Fatalf("expected 1, got %d", len(loaded.Hosts))
	}

	if _, exists := loaded.Hosts["foo"]; !exists {
		t.Errorf("expected something, got nothing")
	}

	if loaded.Hosts["foo"].CertFile != "cert" {
		t.Fatalf("expected cert, got %#v", loaded.Hosts["foo"].CertFile)
	}
}

func TestUpdateDeploymentByName(t *testing.T) {
	deployment := &extensions.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "armor-test",
			Name:      "foo",
		},
	}

	client := fake.NewSimpleClientset()

	if _, err := client.Extensions().Deployments("armor-test").Create(deployment); err != nil {
		t.Fatal(err)
	}

	controller := NewController(client)
	config := &armor.Armor{}

	if err := controller.UpdateDeploymentByName("armor-test", "foo", config); err != nil {
		t.Fatal(err)
	}

	deployment, err := client.Extensions().Deployments("armor-test").Get("foo", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}

	deploymentAnnotation := deployment.Annotations["armor.labstack.com/configHash"]

	if deploymentAnnotation != getConfigHash(config) {
		t.Errorf("expected %#v, got %#v", getConfigHash(config), deploymentAnnotation)
	}

	podAnnotation := deployment.Spec.Template.Annotations["armor.labstack.com/configHash"]

	if podAnnotation != getConfigHash(config) {
		t.Errorf("expected %#v, got %#v", getConfigHash(config), podAnnotation)
	}
}

func TestUpdateDeployment(t *testing.T) {
	deployment := &extensions.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "armor-test",
			Name:      "foo",
		},
	}

	client := fake.NewSimpleClientset()

	if _, err := client.Extensions().Deployments("armor-test").Create(deployment); err != nil {
		t.Fatal(err)
	}

	controller := NewController(client)
	config := &armor.Armor{}

	if err := controller.UpdateDeployment(deployment, config); err != nil {
		t.Fatal(err)
	}

	deployment, err := client.Extensions().Deployments("armor-test").Get("foo", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}

	deploymentAnnotation := deployment.Annotations["armor.labstack.com/configHash"]

	if deploymentAnnotation != getConfigHash(config) {
		t.Errorf("expected %#v, got %#v", getConfigHash(config), deploymentAnnotation)
	}

	podAnnotation := deployment.Spec.Template.Annotations["armor.labstack.com/configHash"]

	if podAnnotation != getConfigHash(config) {
		t.Errorf("expected %#v, got %#v", getConfigHash(config), podAnnotation)
	}
}

func TestUpdateDaemonSetByName(t *testing.T) {
	daemonSet := &extensions.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "armor-test",
			Name:      "foo",
		},
	}

	client := fake.NewSimpleClientset()

	if _, err := client.Extensions().DaemonSets("armor-test").Create(daemonSet); err != nil {
		t.Fatal(err)
	}

	controller := NewController(client)
	config := &armor.Armor{}

	daemonSet, err := controller.UpdateDaemonSetByName("armor-test", "foo", config)
	if err != nil {
		t.Fatal(err)
	}

	daemonSet, err = client.Extensions().DaemonSets("armor-test").Get(daemonSet.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}

	daemonSetAnnotation := daemonSet.Annotations["armor.labstack.com/configHash"]

	if daemonSetAnnotation != getConfigHash(config) {
		t.Errorf("expected %#v, got %#v", getConfigHash(config), daemonSetAnnotation)
	}

	podAnnotation := daemonSet.Spec.Template.Annotations["armor.labstack.com/configHash"]

	if podAnnotation != getConfigHash(config) {
		t.Errorf("expected %#v, got %#v", getConfigHash(config), podAnnotation)
	}
}

func TestUpdateDaemonSet(t *testing.T) {
	daemonSet := &extensions.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "armor-test",
			Name:      "foo",
		},
	}

	client := fake.NewSimpleClientset()

	if _, err := client.Extensions().DaemonSets("armor-test").Create(daemonSet); err != nil {
		t.Fatal(err)
	}

	controller := NewController(client)
	config := &armor.Armor{}

	daemonSet, err := controller.UpdateDaemonSet(daemonSet, config)
	if err != nil {
		t.Fatal(err)
	}

	daemonSet, err = client.Extensions().DaemonSets("armor-test").Get(daemonSet.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}

	daemonSetAnnotation := daemonSet.Annotations["armor.labstack.com/configHash"]

	if daemonSetAnnotation != getConfigHash(config) {
		t.Errorf("expected %#v, got %#v", getConfigHash(config), daemonSetAnnotation)
	}

	podAnnotation := daemonSet.Spec.Template.Annotations["armor.labstack.com/configHash"]

	if podAnnotation != getConfigHash(config) {
		t.Errorf("expected %#v, got %#v", getConfigHash(config), podAnnotation)
	}
}

func TestUpdatePodsByLabelSelector(t *testing.T) {
	config := &armor.Armor{}

	labelSet := labels.Set{
		"app": "armor",
	}

	fixtures := []*v1.Pod{
		// should be removed
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "armor-test",
				Name:      "foo",
				Labels:    labelSet,
			},
		},

		// should not be removed: different namespace
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "armor",
				Name:      "bar",
				Labels:    labelSet,
			},
		},

		// should not be removed: doesn't match labels
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "armor-test",
				Name:      "qux",
			},
		},

		// should not be removed: config up to date
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "armor-test",
				Name:      "waldo",
				Labels:    labelSet,
				Annotations: map[string]string{
					"armor.labstack.com/configHash": getConfigHash(config),
				},
			},
		},
	}

	client := fake.NewSimpleClientset()

	for _, fixture := range fixtures {
		if _, err := client.Core().Pods(fixture.Namespace).Create(fixture); err != nil {
			t.Fatal(err)
		}
	}

	controller := NewController(client)

	if err := controller.UpdatePodsByLabelSelector("armor-test", labels.SelectorFromSet(labelSet), config); err != nil {
		t.Fatal(err)
	}

	pods, err := client.Core().Pods(v1.NamespaceAll).List(metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if len(pods.Items) != 3 {
		t.Fatalf("expected 3, got %d", len(pods.Items))
	}

	if pods.Items[0].Name != "bar" {
		t.Errorf("expected bar, got %#v", pods.Items[0].Name)
	}

	if pods.Items[1].Name != "qux" {
		t.Errorf("expected qux, got %#v", pods.Items[1].Name)
	}

	if pods.Items[2].Name != "waldo" {
		t.Errorf("expected waldo, got %#v", pods.Items[2].Name)
	}
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

	if _, err := client.Core().Nodes().Create(node); err != nil {
		t.Fatal(err)
	}

	controller := NewController(client)

	nodeIPs, err := controller.GetNodeIPs()

	if err != nil {
		t.Fatal(err)
	}

	if len(nodeIPs) != 1 {
		t.Fatalf("expected 1, got %d", len(nodeIPs))
	}

	if nodeIPs[0] != "54.10.11.12" {
		t.Errorf("expected 54.10.11.12, got %#v", nodeIPs[0])
	}
}

func TestGetConfigHash(t *testing.T) {
	config := &armor.Armor{
		Hosts: map[string]*armor.Host{
			"foo": {
				CertFile: "cert",
			},
		},
	}

	otherConfig := &armor.Armor{
		Hosts: map[string]*armor.Host{
			"foo": {
				CertFile: "other-cert",
			},
		},
	}

	if getConfigHash(config) != getConfigHash(config) {
		t.Errorf("equal configs should hash equally")
	}

	if getConfigHash(config) == getConfigHash(otherConfig) {
		t.Errorf("different configs should hash differently")
	}
}

func TestUpstreamServiceURL(t *testing.T) {
	for _, test := range []struct {
		ip, port, want string
	}{
		{"8.8.8.8", "80", "http://8.8.8.8:80"},
		{"1.2.3.4", "8080", "http://1.2.3.4:8080"},
	} {
		got := upstreamServiceURL(test.ip, test.port)
		if got != test.want {
			t.Errorf("upstreamServiceURL(%q, %q) => %q, want %q", test.ip, test.port, got, test.want)
		}
	}
}
