package connectinject

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const k8sNamespace = "k8snamespace"

func TestHandlerContainerInit(t *testing.T) {
	minimal := func() *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "test-namespace",
				Annotations: map[string]string{
					annotationService: "foo",
				},
			},

			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "web",
					},
					{
						Name: "web-side",
					},
				},
			},
			Status: corev1.PodStatus{
				HostIP: "1.1.1.1",
				PodIP:  "2.2.2.2",
			},
		}
	}

	cases := []struct {
		Name    string
		Pod     func(*corev1.Pod) *corev1.Pod
		Handler Handler
		Cmd     string // Strings.Contains test
		CmdNot  string // Not contains
	}{
		// The first test checks the whole template. Subsequent tests check
		// the parts that change.
		{
			"Whole template by default",
			func(pod *corev1.Pod) *corev1.Pod {
				pod.Annotations[annotationService] = "web"
				return pod
			},
			Handler{},
			`/bin/sh -ec 
export CONSUL_HTTP_ADDR="${HOST_IP}:8500"
export CONSUL_GRPC_ADDR="${HOST_IP}:8502"
consul-k8s connect-init -pod-name=${POD_NAME} -pod-namespace=${POD_NAMESPACE} \

# Generate the envoy bootstrap code
/consul/connect-inject/consul connect envoy \
  -proxy-id="$(cat /consul/connect-inject/proxyid)" \
  -bootstrap > /consul/connect-inject/envoy-bootstrap.yaml`,
			"",
		},

		{
			"When auth method is set -service-account-name and -service-name are passed in",
			func(pod *corev1.Pod) *corev1.Pod {
				pod.Annotations[annotationService] = "web"
				pod.Spec.ServiceAccountName = "a-service-account-name"
				pod.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{
					{
						Name:      "sa",
						MountPath: "/var/run/secrets/kubernetes.io/serviceaccount",
					},
				}
				return pod
			},
			Handler{
				AuthMethod: "an-auth-method",
			},
			`/bin/sh -ec 
export CONSUL_HTTP_ADDR="${HOST_IP}:8500"
export CONSUL_GRPC_ADDR="${HOST_IP}:8502"
consul-k8s connect-init -pod-name=${POD_NAME} -pod-namespace=${POD_NAMESPACE} \
  -acl-auth-method="an-auth-method" \
  -service-account-name="a-service-account-name" \
  -service-name="web" \
`,
			"",
		},
		{
			"When running the merged metrics server, configures consul connect envoy command",
			func(pod *corev1.Pod) *corev1.Pod {
				// The annotations to enable metrics, enable merging, and
				// service metrics port make the condition to run the merged
				// metrics server true. When that is the case,
				// prometheusScrapePath and mergedMetricsPort should get
				// rendered as -prometheus-scrape-path and
				// -prometheus-backend-port to the consul connect envoy command.
				pod.Annotations[annotationService] = "web"
				pod.Annotations[annotationEnableMetrics] = "true"
				pod.Annotations[annotationEnableMetricsMerging] = "true"
				pod.Annotations[annotationMergedMetricsPort] = "20100"
				pod.Annotations[annotationServiceMetricsPort] = "1234"
				pod.Annotations[annotationPrometheusScrapePort] = "22222"
				pod.Annotations[annotationPrometheusScrapePath] = "/scrape-path"
				return pod
			},
			Handler{},
			`# Generate the envoy bootstrap code
/consul/connect-inject/consul connect envoy \
  -proxy-id="$(cat /consul/connect-inject/proxyid)" \
  -prometheus-scrape-path="/scrape-path" \
  -prometheus-backend-port="20100" \
  -bootstrap > /consul/connect-inject/envoy-bootstrap.yaml`,
			"",
		},
	}

	for _, tt := range cases {
		t.Run(tt.Name, func(t *testing.T) {
			require := require.New(t)

			h := tt.Handler
			pod := *tt.Pod(minimal())
			container, err := h.containerInit(pod, k8sNamespace)
			require.NoError(err)
			actual := strings.Join(container.Command, " ")
			require.Contains(actual, tt.Cmd)
			if tt.CmdNot != "" {
				require.NotContains(actual, tt.CmdNot)
			}
		})
	}
}

func TestHandlerContainerInit_transparentProxy(t *testing.T) {
	cases := map[string]struct {
		globalEnabled     bool
		annotationEnabled *bool
		expectEnabled     bool
	}{
		"enabled globally, annotation not provided": {
			true,
			nil,
			true,
		},
		"enabled globally, annotation is false": {
			true,
			pointerToBool(false),
			false,
		},
		"enabled globally, annotation is true": {
			true,
			pointerToBool(true),
			true,
		},
		"disabled globally, annotation not provided": {
			false,
			nil,
			false,
		},
		"disabled globally, annotation is false": {
			false,
			pointerToBool(false),
			false,
		},
		"disabled globally, annotation is true": {
			false,
			pointerToBool(true),
			true,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := Handler{EnableTransparentProxy: c.globalEnabled}
			pod := minimal()
			if c.annotationEnabled != nil {
				pod.Annotations[annotationTransparentProxy] = strconv.FormatBool(*c.annotationEnabled)
			}

			expectedSecurityContext := &corev1.SecurityContext{
				RunAsUser:  pointerToInt64(0),
				RunAsGroup: pointerToInt64(0),
				Capabilities: &corev1.Capabilities{
					Add: []corev1.Capability{netAdminCapability},
				},
				RunAsNonRoot: pointerToBool(false),
			}
			expectedCmd := `/consul/connect-inject/consul connect redirect-traffic \
  -proxy-id="$(cat /consul/connect-inject/proxyid)" \
  -proxy-uid=5995`
			container, err := h.containerInit(*pod, k8sNamespace)
			require.NoError(t, err)
			actualCmd := strings.Join(container.Command, " ")

			if c.expectEnabled {
				require.Equal(t, expectedSecurityContext, container.SecurityContext)
				require.Contains(t, actualCmd, expectedCmd)
			} else {
				require.Nil(t, container.SecurityContext)
				require.NotContains(t, actualCmd, expectedCmd)
			}
		})
	}
}

func TestHandlerContainerInit_namespacesEnabled(t *testing.T) {
	minimal := func() *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotationService: "foo",
				},
			},

			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "web",
					},
					{
						Name: "web-side",
					},
					{
						Name: "auth-method-secret",
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "service-account-secret",
								MountPath: "/var/run/secrets/kubernetes.io/serviceaccount",
							},
						},
					},
				},
				ServiceAccountName: "web",
			},
		}
	}

	cases := []struct {
		Name    string
		Pod     func(*corev1.Pod) *corev1.Pod
		Handler Handler
		Cmd     string // Strings.Contains test
	}{
		{
			"whole template, default namespace",
			func(pod *corev1.Pod) *corev1.Pod {
				pod.Annotations[annotationService] = "web"
				return pod
			},
			Handler{
				EnableNamespaces:           true,
				ConsulDestinationNamespace: "default",
			},
			`/bin/sh -ec 
export CONSUL_HTTP_ADDR="${HOST_IP}:8500"
export CONSUL_GRPC_ADDR="${HOST_IP}:8502"
consul-k8s connect-init -pod-name=${POD_NAME} -pod-namespace=${POD_NAMESPACE} \
  -consul-service-namespace="default" \

# Generate the envoy bootstrap code
/consul/connect-inject/consul connect envoy \
  -proxy-id="$(cat /consul/connect-inject/proxyid)" \
  -namespace="default" \
  -bootstrap > /consul/connect-inject/envoy-bootstrap.yaml`,
		},

		{
			"whole template, non-default namespace",
			func(pod *corev1.Pod) *corev1.Pod {
				pod.Annotations[annotationService] = "web"
				return pod
			},
			Handler{
				EnableNamespaces:           true,
				ConsulDestinationNamespace: "non-default",
			},
			`/bin/sh -ec 
export CONSUL_HTTP_ADDR="${HOST_IP}:8500"
export CONSUL_GRPC_ADDR="${HOST_IP}:8502"
consul-k8s connect-init -pod-name=${POD_NAME} -pod-namespace=${POD_NAMESPACE} \
  -consul-service-namespace="non-default" \

# Generate the envoy bootstrap code
/consul/connect-inject/consul connect envoy \
  -proxy-id="$(cat /consul/connect-inject/proxyid)" \
  -namespace="non-default" \
  -bootstrap > /consul/connect-inject/envoy-bootstrap.yaml`,
		},

		{
			"Whole template, auth method, non-default namespace, mirroring disabled",
			func(pod *corev1.Pod) *corev1.Pod {
				pod.Annotations[annotationService] = ""
				return pod
			},
			Handler{
				AuthMethod:                 "auth-method",
				EnableNamespaces:           true,
				ConsulDestinationNamespace: "non-default",
			},
			`/bin/sh -ec 
export CONSUL_HTTP_ADDR="${HOST_IP}:8500"
export CONSUL_GRPC_ADDR="${HOST_IP}:8502"
consul-k8s connect-init -pod-name=${POD_NAME} -pod-namespace=${POD_NAMESPACE} \
  -acl-auth-method="auth-method" \
  -service-account-name="web" \
  -service-name="" \
  -auth-method-namespace="non-default" \
  -consul-service-namespace="non-default" \

# Generate the envoy bootstrap code
/consul/connect-inject/consul connect envoy \
  -proxy-id="$(cat /consul/connect-inject/proxyid)" \
  -token-file="/consul/connect-inject/acl-token" \
  -namespace="non-default" \
  -bootstrap > /consul/connect-inject/envoy-bootstrap.yaml`,
		},
		{
			"Whole template, auth method, non-default namespace, mirroring enabled",
			func(pod *corev1.Pod) *corev1.Pod {
				pod.Annotations[annotationService] = ""
				return pod
			},
			Handler{
				AuthMethod:                 "auth-method",
				EnableNamespaces:           true,
				ConsulDestinationNamespace: "non-default", // Overridden by mirroring
				EnableK8SNSMirroring:       true,
			},
			`/bin/sh -ec 
export CONSUL_HTTP_ADDR="${HOST_IP}:8500"
export CONSUL_GRPC_ADDR="${HOST_IP}:8502"
consul-k8s connect-init -pod-name=${POD_NAME} -pod-namespace=${POD_NAMESPACE} \
  -acl-auth-method="auth-method" \
  -service-account-name="web" \
  -service-name="" \
  -auth-method-namespace="default" \
  -consul-service-namespace="k8snamespace" \

# Generate the envoy bootstrap code
/consul/connect-inject/consul connect envoy \
  -proxy-id="$(cat /consul/connect-inject/proxyid)" \
  -token-file="/consul/connect-inject/acl-token" \
  -namespace="k8snamespace" \
  -bootstrap > /consul/connect-inject/envoy-bootstrap.yaml`,
		},
		{
			"whole template, default namespace, tproxy enabled",
			func(pod *corev1.Pod) *corev1.Pod {
				pod.Annotations[annotationService] = "web"
				return pod
			},
			Handler{
				EnableNamespaces:           true,
				ConsulDestinationNamespace: "default",
				EnableTransparentProxy:     true,
			},
			`/bin/sh -ec 
export CONSUL_HTTP_ADDR="${HOST_IP}:8500"
export CONSUL_GRPC_ADDR="${HOST_IP}:8502"
consul-k8s connect-init -pod-name=${POD_NAME} -pod-namespace=${POD_NAMESPACE} \
  -consul-service-namespace="default" \

# Generate the envoy bootstrap code
/consul/connect-inject/consul connect envoy \
  -proxy-id="$(cat /consul/connect-inject/proxyid)" \
  -namespace="default" \
  -bootstrap > /consul/connect-inject/envoy-bootstrap.yaml

# Apply traffic redirection rules.
/consul/connect-inject/consul connect redirect-traffic \
  -namespace="default" \
  -proxy-id="$(cat /consul/connect-inject/proxyid)" \
  -proxy-uid=5995`,
		},

		{
			"whole template, non-default namespace, tproxy enabled",
			func(pod *corev1.Pod) *corev1.Pod {
				pod.Annotations[annotationService] = "web"
				return pod
			},
			Handler{
				EnableNamespaces:           true,
				ConsulDestinationNamespace: "non-default",
				EnableTransparentProxy:     true,
			},
			`/bin/sh -ec 
export CONSUL_HTTP_ADDR="${HOST_IP}:8500"
export CONSUL_GRPC_ADDR="${HOST_IP}:8502"
consul-k8s connect-init -pod-name=${POD_NAME} -pod-namespace=${POD_NAMESPACE} \
  -consul-service-namespace="non-default" \

# Generate the envoy bootstrap code
/consul/connect-inject/consul connect envoy \
  -proxy-id="$(cat /consul/connect-inject/proxyid)" \
  -namespace="non-default" \
  -bootstrap > /consul/connect-inject/envoy-bootstrap.yaml

# Apply traffic redirection rules.
/consul/connect-inject/consul connect redirect-traffic \
  -namespace="non-default" \
  -proxy-id="$(cat /consul/connect-inject/proxyid)" \
  -proxy-uid=5995`,
		},

		{
			"Whole template, auth method, non-default namespace, mirroring enabled, tproxy enabled",
			func(pod *corev1.Pod) *corev1.Pod {
				pod.Annotations[annotationService] = "web"
				return pod
			},
			Handler{
				AuthMethod:                 "auth-method",
				EnableNamespaces:           true,
				ConsulDestinationNamespace: "non-default", // Overridden by mirroring
				EnableK8SNSMirroring:       true,
				EnableTransparentProxy:     true,
			},
			`/bin/sh -ec 
export CONSUL_HTTP_ADDR="${HOST_IP}:8500"
export CONSUL_GRPC_ADDR="${HOST_IP}:8502"
consul-k8s connect-init -pod-name=${POD_NAME} -pod-namespace=${POD_NAMESPACE} \
  -acl-auth-method="auth-method" \
  -service-account-name="web" \
  -service-name="web" \
  -auth-method-namespace="default" \
  -consul-service-namespace="k8snamespace" \

# Generate the envoy bootstrap code
/consul/connect-inject/consul connect envoy \
  -proxy-id="$(cat /consul/connect-inject/proxyid)" \
  -token-file="/consul/connect-inject/acl-token" \
  -namespace="k8snamespace" \
  -bootstrap > /consul/connect-inject/envoy-bootstrap.yaml

# Apply traffic redirection rules.
/consul/connect-inject/consul connect redirect-traffic \
  -namespace="k8snamespace" \
  -proxy-id="$(cat /consul/connect-inject/proxyid)" \
  -proxy-uid=5995`,
		},
	}

	for _, tt := range cases {
		t.Run(tt.Name, func(t *testing.T) {
			require := require.New(t)

			h := tt.Handler
			container, err := h.containerInit(*tt.Pod(minimal()), k8sNamespace)
			require.NoError(err)
			actual := strings.Join(container.Command, " ")
			require.Equal(tt.Cmd, actual)
		})
	}
}

func TestHandlerContainerInit_authMethod(t *testing.T) {
	require := require.New(t)
	h := Handler{
		AuthMethod: "release-name-consul-k8s-auth-method",
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				annotationService: "foo",
			},
		},

		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "web",
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "default-token-podid",
							ReadOnly:  true,
							MountPath: "/var/run/secrets/kubernetes.io/serviceaccount",
						},
					},
				},
			},
			ServiceAccountName: "foo",
		},
	}
	container, err := h.containerInit(*pod, k8sNamespace)
	require.NoError(err)
	actual := strings.Join(container.Command, " ")
	require.Contains(actual, `
consul-k8s connect-init -pod-name=${POD_NAME} -pod-namespace=${POD_NAMESPACE} \
  -acl-auth-method="release-name-consul-k8s-auth-method"`)
	require.Contains(actual, `
# Generate the envoy bootstrap code
/consul/connect-inject/consul connect envoy \
  -proxy-id="$(cat /consul/connect-inject/proxyid)" \
  -token-file="/consul/connect-inject/acl-token" \
  -bootstrap > /consul/connect-inject/envoy-bootstrap.yaml`)
}

// If Consul CA cert is set,
// Consul addresses should use HTTPS
// and CA cert should be set as env variable
func TestHandlerContainerInit_WithTLS(t *testing.T) {
	require := require.New(t)
	h := Handler{
		ConsulCACert: "consul-ca-cert",
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				annotationService: "foo",
			},
		},

		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "web",
				},
			},
		},
	}
	container, err := h.containerInit(*pod, k8sNamespace)
	require.NoError(err)
	actual := strings.Join(container.Command, " ")
	require.Contains(actual, `
export CONSUL_HTTP_ADDR="https://${HOST_IP}:8501"
export CONSUL_GRPC_ADDR="https://${HOST_IP}:8502"
export CONSUL_CACERT=/consul/connect-inject/consul-ca.pem
cat <<EOF >/consul/connect-inject/consul-ca.pem
consul-ca-cert
EOF`)
	require.NotContains(actual, `
export CONSUL_HTTP_ADDR="${HOST_IP}:8500"
export CONSUL_GRPC_ADDR="${HOST_IP}:8502"`)
}

func TestHandlerContainerInit_Resources(t *testing.T) {
	require := require.New(t)
	h := Handler{
		InitContainerResources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("10Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("20m"),
				corev1.ResourceMemory: resource.MustParse("25Mi"),
			},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				annotationService: "foo",
			},
		},

		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "web",
				},
			},
		},
	}
	container, err := h.containerInit(*pod, k8sNamespace)
	require.NoError(err)
	require.Equal(corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("20m"),
			corev1.ResourceMemory: resource.MustParse("25Mi"),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("10m"),
			corev1.ResourceMemory: resource.MustParse("10Mi"),
		},
	}, container.Resources)
}

// Test that the init copy container has the correct command and SecurityContext.
func TestHandlerContainerInitCopyContainer(t *testing.T) {
	require := require.New(t)
	h := Handler{}
	container := h.containerInitCopyContainer()
	expectedSecurityContext := &corev1.SecurityContext{
		RunAsUser:              pointerToInt64(copyContainerUserAndGroupID),
		RunAsGroup:             pointerToInt64(copyContainerUserAndGroupID),
		RunAsNonRoot:           pointerToBool(true),
		ReadOnlyRootFilesystem: pointerToBool(true),
	}
	require.Equal(container.SecurityContext, expectedSecurityContext)
	actual := strings.Join(container.Command, " ")
	require.Contains(actual, `cp /bin/consul /consul/connect-inject/consul`)
}
