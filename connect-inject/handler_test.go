package connectinject

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mapset "github.com/deckarep/golang-set"
	logrtest "github.com/go-logr/logr/testing"
	"github.com/stretchr/testify/require"
	"gomodules.xyz/jsonpatch/v2"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func TestHandlerHandle(t *testing.T) {
	t.Parallel()
	basicSpec := corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name: "web",
			},
		},
	}
	s := runtime.NewScheme()
	s.AddKnownTypes(schema.GroupVersion{
		Group:   "",
		Version: "v1",
	}, &corev1.Pod{})
	decoder, err := admission.NewDecoder(s)
	require.NoError(t, err)

	cases := []struct {
		Name    string
		Handler Handler
		Req     admission.Request
		Err     string // expected error string, not exact
		Patches []jsonpatch.Operation
	}{
		{
			"kube-system namespace",
			Handler{
				Log:                   logrtest.TestLogger{T: t},
				AllowK8sNamespacesSet: mapset.NewSetWith("*"),
				DenyK8sNamespacesSet:  mapset.NewSet(),
				decoder:               decoder,
			},
			admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Namespace: metav1.NamespaceSystem,
					Object: encodeRaw(t, &corev1.Pod{
						Spec: basicSpec,
					}),
				},
			},
			"",
			nil,
		},

		{
			"already injected",
			Handler{
				Log:                   logrtest.TestLogger{T: t},
				AllowK8sNamespacesSet: mapset.NewSetWith("*"),
				DenyK8sNamespacesSet:  mapset.NewSet(),
				decoder:               decoder,
			},
			admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: encodeRaw(t, &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								keyInjectStatus: injected,
							},
						},
						Spec: basicSpec,
					}),
				},
			},
			"",
			nil,
		},

		{
			"empty pod basic",
			Handler{
				Log:                   logrtest.TestLogger{T: t},
				AllowK8sNamespacesSet: mapset.NewSetWith("*"),
				DenyK8sNamespacesSet:  mapset.NewSet(),
				decoder:               decoder,
			},
			admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: encodeRaw(t, &corev1.Pod{
						Spec: basicSpec,
					}),
				},
			},
			"",
			[]jsonpatch.Operation{
				{
					Operation: "add",
					Path:      "/metadata/labels",
				},
				{
					Operation: "add",
					Path:      "/metadata/annotations",
				},
				{
					Operation: "add",
					Path:      "/spec/volumes",
				},
				{
					Operation: "add",
					Path:      "/spec/initContainers",
				},
				{
					Operation: "add",
					Path:      "/spec/containers/1",
				},
			},
		},

		// todo: why is upstreams different then basic
		{
			"pod with upstreams specified",
			Handler{
				Log:                   logrtest.TestLogger{T: t},
				AllowK8sNamespacesSet: mapset.NewSetWith("*"),
				DenyK8sNamespacesSet:  mapset.NewSet(),
				decoder:               decoder,
			},
			admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: encodeRaw(t, &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								annotationUpstreams: "echo:1234,db:1234",
							},
						},
						Spec: basicSpec,
					}),
				},
			},
			"",
			[]jsonpatch.Operation{
				{
					Operation: "add",
					Path:      "/metadata/labels",
				},
				{
					Operation: "add",
					Path:      "/metadata/annotations/" + escapeJSONPointer(keyInjectStatus),
				},
				{
					Operation: "add",
					Path:      "/spec/volumes",
				},
				{
					Operation: "add",
					Path:      "/spec/initContainers",
				},
				{
					Operation: "add",
					Path:      "/spec/containers/1",
				},
			},
		},

		{
			"empty pod with injection disabled",
			Handler{
				Log:                   logrtest.TestLogger{T: t},
				AllowK8sNamespacesSet: mapset.NewSetWith("*"),
				DenyK8sNamespacesSet:  mapset.NewSet(),
				decoder:               decoder,
			},
			admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: encodeRaw(t, &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								annotationInject: "false",
							},
						},
						Spec: basicSpec,
					}),
				},
			},
			"",
			nil,
		},

		{
			"empty pod with injection truthy",
			Handler{
				Log:                   logrtest.TestLogger{T: t},
				AllowK8sNamespacesSet: mapset.NewSetWith("*"),
				DenyK8sNamespacesSet:  mapset.NewSet(),
				decoder:               decoder,
			},
			admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: encodeRaw(t, &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								annotationInject: "t",
							},
						},
						Spec: basicSpec,
					}),
				},
			},
			"",
			[]jsonpatch.Operation{
				{
					Operation: "add",
					Path:      "/spec/volumes",
				},
				{
					Operation: "add",
					Path:      "/spec/initContainers",
				},
				{
					Operation: "add",
					Path:      "/spec/containers/1",
				},
				{
					Operation: "add",
					Path:      "/metadata/annotations/" + escapeJSONPointer(keyInjectStatus),
				},
				{
					Operation: "add",
					Path:      "/metadata/labels",
				},
			},
		},

		{
			"pod with service annotation",
			Handler{
				Log:                   logrtest.TestLogger{T: t},
				AllowK8sNamespacesSet: mapset.NewSetWith("*"),
				DenyK8sNamespacesSet:  mapset.NewSet(),
				decoder:               decoder,
			},
			admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: encodeRaw(t, &corev1.Pod{
						Spec: basicSpec,
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								annotationService: "foo",
							},
						},
					}),
				},
			},
			"",
			[]jsonpatch.Operation{
				{
					Operation: "add",
					Path:      "/spec/volumes",
				},
				{
					Operation: "add",
					Path:      "/spec/initContainers",
				},
				{
					Operation: "add",
					Path:      "/spec/containers/1",
				},
				{
					Operation: "add",
					Path:      "/metadata/annotations/" + escapeJSONPointer(keyInjectStatus),
				},
				{
					Operation: "add",
					Path:      "/metadata/labels",
				},
			},
		},

		{
			"pod with existing label",
			Handler{
				Log:                   logrtest.TestLogger{T: t},
				AllowK8sNamespacesSet: mapset.NewSetWith("*"),
				DenyK8sNamespacesSet:  mapset.NewSet(),
				decoder:               decoder,
			},
			admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: encodeRaw(t, &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"testLabel": "123",
							},
						},
						Spec: basicSpec,
					}),
				},
			},
			"",
			[]jsonpatch.Operation{
				{
					Operation: "add",
					Path:      "/spec/volumes",
				},
				{
					Operation: "add",
					Path:      "/spec/initContainers",
				},
				{
					Operation: "add",
					Path:      "/spec/containers/1",
				},
				{
					Operation: "add",
					Path:      "/metadata/annotations",
				},
				{
					Operation: "add",
					Path:      "/metadata/labels/" + escapeJSONPointer(keyInjectStatus),
				},
			},
		},

		{
			"when metrics merging is enabled, we should inject the consul-sidecar and add prometheus annotations",
			Handler{
				Log:                   logrtest.TestLogger{T: t},
				AllowK8sNamespacesSet: mapset.NewSetWith("*"),
				DenyK8sNamespacesSet:  mapset.NewSet(),
				MetricsConfig: MetricsConfig{
					DefaultEnableMetrics:        true,
					DefaultEnableMetricsMerging: true,
				},
				decoder: decoder,
			},
			admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: encodeRaw(t, &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"testLabel": "123",
							},
							Annotations: map[string]string{
								annotationServiceMetricsPort: "1234",
							},
						},
						Spec: basicSpec,
					}),
				},
			},
			"",
			[]jsonpatch.Operation{
				{
					Operation: "add",
					Path:      "/spec/volumes",
				},
				{
					Operation: "add",
					Path:      "/spec/initContainers",
				},
				{
					Operation: "add",
					Path:      "/spec/containers/1",
				},
				{
					Operation: "add",
					Path:      "/spec/containers/2",
				},
				{
					Operation: "add",
					Path:      "/metadata/annotations/" + escapeJSONPointer(keyInjectStatus),
				},
				{
					Operation: "add",
					Path:      "/metadata/annotations/" + escapeJSONPointer(annotationPrometheusScrape),
				},
				{
					Operation: "add",
					Path:      "/metadata/annotations/" + escapeJSONPointer(annotationPrometheusPath),
				},
				{
					Operation: "add",
					Path:      "/metadata/annotations/" + escapeJSONPointer(annotationPrometheusPort),
				},
				{
					Operation: "add",
					Path:      "/metadata/labels/" + escapeJSONPointer(keyInjectStatus),
				},
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.Name, func(t *testing.T) {
			require := require.New(t)
			ctx := context.Background()
			resp := tt.Handler.Handle(ctx, tt.Req)
			if (tt.Err == "") != resp.Allowed {
				t.Fatalf("allowed: %v, expected err: %v", resp.Allowed, tt.Err)
			}
			if tt.Err != "" {
				require.Contains(resp.Result.Message, tt.Err)
				return
			}

			actual := resp.Patches
			if len(actual) > 0 {
				for i, _ := range actual {
					actual[i].Value = nil
				}
			}
			require.ElementsMatch(tt.Patches, actual)
		})
	}
}

// Test that we error out when deprecated annotations are set.
func TestHandler_ErrorsOnDeprecatedAnnotations(t *testing.T) {
	cases := []struct {
		name        string
		annotations map[string]string
		expErr      string
	}{
		{
			"default protocol annotation",
			map[string]string{
				annotationProtocol: "http",
			},
			"the \"consul.hashicorp.com/connect-service-protocol\" annotation is no longer supported. Instead, create a ServiceDefaults resource (see www.consul.io/docs/k8s/crds/upgrade-to-crds)",
		},
		{
			"sync period annotation",
			map[string]string{
				annotationSyncPeriod: "30s",
			},
			"the \"consul.hashicorp.com/connect-sync-period\" annotation is no longer supported because consul-sidecar is no longer injected to periodically register services",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require := require.New(t)
			s := runtime.NewScheme()
			s.AddKnownTypes(schema.GroupVersion{
				Group:   "",
				Version: "v1",
			}, &corev1.Pod{})
			decoder, err := admission.NewDecoder(s)
			require.NoError(err)

			handler := Handler{
				Log:                   logrtest.TestLogger{T: t},
				AllowK8sNamespacesSet: mapset.NewSetWith("*"),
				DenyK8sNamespacesSet:  mapset.NewSet(),
				decoder:               decoder,
			}

			request := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Namespace: "default",
					Object: encodeRaw(t, &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: c.annotations,
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "web",
								},
							},
						},
					}),
				},
			}

			response := handler.Handle(context.Background(), request)
			require.False(response.Allowed)
			require.Equal(c.expErr, response.Result.Message)
		})
	}
}

func TestHandlerDefaultAnnotations(t *testing.T) {
	cases := []struct {
		Name     string
		Pod      *corev1.Pod
		Expected map[string]string
		Err      string
	}{
		{
			"empty",
			&corev1.Pod{},
			nil,
			"",
		},

		{
			"basic pod, no ports",
			&corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						corev1.Container{
							Name: "web",
						},

						corev1.Container{
							Name: "web-side",
						},
					},
				},
			},
			nil,
			"",
		},

		{
			"basic pod, name annotated",
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotationService: "foo",
					},
				},

				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						corev1.Container{
							Name: "web",
						},

						corev1.Container{
							Name: "web-side",
						},
					},
				},
			},
			map[string]string{
				annotationService: "foo",
			},
			"",
		},

		{
			"basic pod, with ports",
			&corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						corev1.Container{
							Name: "web",
							Ports: []corev1.ContainerPort{
								corev1.ContainerPort{
									Name:          "http",
									ContainerPort: 8080,
								},
							},
						},

						corev1.Container{
							Name: "web-side",
						},
					},
				},
			},
			map[string]string{
				annotationPort: "http",
			},
			"",
		},

		{
			"basic pod, with unnamed ports",
			&corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						corev1.Container{
							Name: "web",
							Ports: []corev1.ContainerPort{
								corev1.ContainerPort{
									ContainerPort: 8080,
								},
							},
						},

						corev1.Container{
							Name: "web-side",
						},
					},
				},
			},
			map[string]string{
				annotationPort: "8080",
			},
			"",
		},
	}

	for _, tt := range cases {
		t.Run(tt.Name, func(t *testing.T) {
			require := require.New(t)

			var h Handler
			err := h.defaultAnnotations(tt.Pod)
			if (tt.Err != "") != (err != nil) {
				t.Fatalf("actual: %v, expected err: %v", err, tt.Err)
			}
			if tt.Err != "" {
				require.Contains(err.Error(), tt.Err)
				return
			}

			actual := tt.Pod.Annotations
			if len(actual) == 0 {
				actual = nil
			}
			require.Equal(tt.Expected, actual)
		})
	}
}

func TestHandlerPrometheusAnnotations(t *testing.T) {
	cases := []struct {
		Name     string
		Handler  Handler
		Expected map[string]string
	}{
		{
			Name: "Sets the correct prometheus annotations on the pod if metrics are enabled",
			Handler: Handler{
				MetricsConfig: MetricsConfig{
					DefaultEnableMetrics:        true,
					DefaultPrometheusScrapePort: "20200",
					DefaultPrometheusScrapePath: "/metrics",
				},
			},
			Expected: map[string]string{
				annotationPrometheusScrape: "true",
				annotationPrometheusPort:   "20200",
				annotationPrometheusPath:   "/metrics",
			},
		},
		{
			Name: "Does not set annotations if metrics are not enabled",
			Handler: Handler{
				MetricsConfig: MetricsConfig{
					DefaultEnableMetrics:        false,
					DefaultPrometheusScrapePort: "20200",
					DefaultPrometheusScrapePath: "/metrics",
				},
			},
			Expected: map[string]string{},
		},
	}

	for _, tt := range cases {
		t.Run(tt.Name, func(t *testing.T) {
			require := require.New(t)
			h := tt.Handler
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}}}

			err := h.prometheusAnnotations(pod)
			require.NoError(err)

			require.Equal(pod.Annotations, tt.Expected)
		})
	}
}

// Test portValue function
func TestHandlerPortValue(t *testing.T) {
	cases := []struct {
		Name     string
		Pod      *corev1.Pod
		Value    string
		Expected int32
		Err      string
	}{
		{
			"empty",
			&corev1.Pod{},
			"",
			0,
			"strconv.ParseInt: parsing \"\": invalid syntax",
		},

		{
			"basic pod, with ports",
			&corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						corev1.Container{
							Name: "web",
							Ports: []corev1.ContainerPort{
								corev1.ContainerPort{
									Name:          "http",
									ContainerPort: 8080,
								},
							},
						},

						corev1.Container{
							Name: "web-side",
						},
					},
				},
			},
			"http",
			int32(8080),
			"",
		},

		{
			"basic pod, with unnamed ports",
			&corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						corev1.Container{
							Name: "web",
							Ports: []corev1.ContainerPort{
								corev1.ContainerPort{
									ContainerPort: 8080,
								},
							},
						},

						corev1.Container{
							Name: "web-side",
						},
					},
				},
			},
			"8080",
			int32(8080),
			"",
		},
	}

	for _, tt := range cases {
		t.Run(tt.Name, func(t *testing.T) {
			require := require.New(t)

			port, err := portValue(*tt.Pod, tt.Value)
			if (tt.Err != "") != (err != nil) {
				t.Fatalf("actual: %v, expected err: %v", err, tt.Err)
			}
			if tt.Err != "" {
				require.Contains(err.Error(), tt.Err)
				return
			}

			require.Equal(tt.Expected, port)
		})
	}
}

// Test consulNamespace function
func TestConsulNamespace(t *testing.T) {
	cases := []struct {
		Name                       string
		EnableNamespaces           bool
		ConsulDestinationNamespace string
		EnableK8SNSMirroring       bool
		K8SNSMirroringPrefix       string
		K8sNamespace               string
		Expected                   string
	}{
		{
			"namespaces disabled",
			false,
			"default",
			false,
			"",
			"namespace",
			"",
		},

		{
			"namespaces disabled, mirroring enabled",
			false,
			"default",
			true,
			"",
			"namespace",
			"",
		},

		{
			"namespaces disabled, mirroring enabled, prefix defined",
			false,
			"default",
			true,
			"test-",
			"namespace",
			"",
		},

		{
			"namespaces enabled, mirroring disabled",
			true,
			"default",
			false,
			"",
			"namespace",
			"default",
		},

		{
			"namespaces enabled, mirroring disabled, prefix defined",
			true,
			"default",
			false,
			"test-",
			"namespace",
			"default",
		},

		{
			"namespaces enabled, mirroring enabled",
			true,
			"default",
			true,
			"",
			"namespace",
			"namespace",
		},

		{
			"namespaces enabled, mirroring enabled, prefix defined",
			true,
			"default",
			true,
			"test-",
			"namespace",
			"test-namespace",
		},
	}

	for _, tt := range cases {
		t.Run(tt.Name, func(t *testing.T) {
			require := require.New(t)

			h := Handler{
				EnableNamespaces:           tt.EnableNamespaces,
				ConsulDestinationNamespace: tt.ConsulDestinationNamespace,
				EnableK8SNSMirroring:       tt.EnableK8SNSMirroring,
				K8SNSMirroringPrefix:       tt.K8SNSMirroringPrefix,
			}

			ns := h.consulNamespace(tt.K8sNamespace)

			require.Equal(tt.Expected, ns)
		})
	}
}

// Test shouldInject function
func TestShouldInject(t *testing.T) {
	cases := []struct {
		Name                  string
		Pod                   *corev1.Pod
		K8sNamespace          string
		EnableNamespaces      bool
		AllowK8sNamespacesSet mapset.Set
		DenyK8sNamespacesSet  mapset.Set
		Expected              bool
	}{
		{
			"kube-system not injected",
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						// Service annotation is required for injection
						annotationService: "testing",
					},
				},
			},
			"kube-system",
			false,
			mapset.NewSet(),
			mapset.NewSet(),
			false,
		},
		{
			"kube-public not injected",
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotationService: "testing",
					},
				},
			},
			"kube-public",
			false,
			mapset.NewSet(),
			mapset.NewSet(),
			false,
		},
		{
			"namespaces disabled, empty allow/deny lists",
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotationService: "testing",
					},
				},
			},
			"default",
			false,
			mapset.NewSet(),
			mapset.NewSet(),
			false,
		},
		{
			"namespaces disabled, allow *",
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotationService: "testing",
					},
				},
			},
			"default",
			false,
			mapset.NewSetWith("*"),
			mapset.NewSet(),
			true,
		},
		{
			"namespaces disabled, allow default",
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotationService: "testing",
					},
				},
			},
			"default",
			false,
			mapset.NewSetWith("default"),
			mapset.NewSet(),
			true,
		},
		{
			"namespaces disabled, allow * and default",
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotationService: "testing",
					},
				},
			},
			"default",
			false,
			mapset.NewSetWith("*", "default"),
			mapset.NewSet(),
			true,
		},
		{
			"namespaces disabled, allow only ns1 and ns2",
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotationService: "testing",
					},
				},
			},
			"default",
			false,
			mapset.NewSetWith("ns1", "ns2"),
			mapset.NewSet(),
			false,
		},
		{
			"namespaces disabled, deny default ns",
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotationService: "testing",
					},
				},
			},
			"default",
			false,
			mapset.NewSet(),
			mapset.NewSetWith("default"),
			false,
		},
		{
			"namespaces disabled, allow *, deny default ns",
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotationService: "testing",
					},
				},
			},
			"default",
			false,
			mapset.NewSetWith("*"),
			mapset.NewSetWith("default"),
			false,
		},
		{
			"namespaces disabled, default ns in both allow and deny lists",
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotationService: "testing",
					},
				},
			},
			"default",
			false,
			mapset.NewSetWith("default"),
			mapset.NewSetWith("default"),
			false,
		},
		{
			"namespaces enabled, empty allow/deny lists",
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotationService: "testing",
					},
				},
			},
			"default",
			true,
			mapset.NewSet(),
			mapset.NewSet(),
			false,
		},
		{
			"namespaces enabled, allow *",
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotationService: "testing",
					},
				},
			},
			"default",
			true,
			mapset.NewSetWith("*"),
			mapset.NewSet(),
			true,
		},
		{
			"namespaces enabled, allow default",
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotationService: "testing",
					},
				},
			},
			"default",
			true,
			mapset.NewSetWith("default"),
			mapset.NewSet(),
			true,
		},
		{
			"namespaces enabled, allow * and default",
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotationService: "testing",
					},
				},
			},
			"default",
			true,
			mapset.NewSetWith("*", "default"),
			mapset.NewSet(),
			true,
		},
		{
			"namespaces enabled, allow only ns1 and ns2",
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotationService: "testing",
					},
				},
			},
			"default",
			true,
			mapset.NewSetWith("ns1", "ns2"),
			mapset.NewSet(),
			false,
		},
		{
			"namespaces enabled, deny default ns",
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotationService: "testing",
					},
				},
			},
			"default",
			true,
			mapset.NewSet(),
			mapset.NewSetWith("default"),
			false,
		},
		{
			"namespaces enabled, allow *, deny default ns",
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotationService: "testing",
					},
				},
			},
			"default",
			true,
			mapset.NewSetWith("*"),
			mapset.NewSetWith("default"),
			false,
		},
		{
			"namespaces enabled, default ns in both allow and deny lists",
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotationService: "testing",
					},
				},
			},
			"default",
			true,
			mapset.NewSetWith("default"),
			mapset.NewSetWith("default"),
			false,
		},
	}

	for _, tt := range cases {
		t.Run(tt.Name, func(t *testing.T) {
			require := require.New(t)

			h := Handler{
				RequireAnnotation:     false,
				EnableNamespaces:      tt.EnableNamespaces,
				AllowK8sNamespacesSet: tt.AllowK8sNamespacesSet,
				DenyK8sNamespacesSet:  tt.DenyK8sNamespacesSet,
			}

			injected, err := h.shouldInject(*tt.Pod, tt.K8sNamespace)

			require.Equal(nil, err)
			require.Equal(tt.Expected, injected)
		})
	}
}

// encodeRaw is a helper to encode some data into a RawExtension.
func encodeRaw(t *testing.T, input interface{}) runtime.RawExtension {
	data, err := json.Marshal(input)
	require.NoError(t, err)
	return runtime.RawExtension{Raw: data}
}

// https://tools.ietf.org/html/rfc6901
func escapeJSONPointer(s string) string {
	s = strings.Replace(s, "~", "~0", -1)
	s = strings.Replace(s, "/", "~1", -1)
	return s
}
