// +build enterprise

package connectinject

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/deckarep/golang-set"
	logrtest "github.com/go-logr/logr/testing"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/hashicorp/consul-k8s/namespaces"
	"github.com/hashicorp/consul-k8s/subcommand/common"
	"github.com/hashicorp/consul/api"
	"github.com/hashicorp/consul/sdk/testutil"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestReconcileCreateEndpoint tests the logic to create service instances in Consul from the addresses in the Endpoints
// object. The cases test a basic endpoints object with two addresses. This test verifies that the services and their TTL
// health checks are created in the expected Consul namespace for various combinations of namespace flags.
// This test covers EndpointsController.createServiceRegistrations.
func TestReconcileCreateEndpointWithNamespaces(t *testing.T) {
	t.Parallel()
	nodeName := "test-node"
	cases := map[string]struct {
		Mirror       bool
		MirrorPrefix string
		SourceKubeNS string
		DestConsulNS string
		ExpConsulNS  string
	}{
		"SourceKubeNS=default, DestConsulNS=default": {
			SourceKubeNS: "default",
			DestConsulNS: "default",
			ExpConsulNS:  "default",
		},
		"SourceKubeNS=kube, DestConsulNS=default": {
			SourceKubeNS: "kube",
			DestConsulNS: "default",
			ExpConsulNS:  "default",
		},
		"SourceKubeNS=default, DestConsulNS=other": {
			SourceKubeNS: "default",
			DestConsulNS: "other",
			ExpConsulNS:  "other",
		},
		"SourceKubeNS=kube, DestConsulNS=other": {
			SourceKubeNS: "kube",
			DestConsulNS: "other",
			ExpConsulNS:  "other",
		},
		"SourceKubeNS=default, Mirror=true": {
			SourceKubeNS: "default",
			Mirror:       true,
			ExpConsulNS:  "default",
		},
		"SourceKubeNS=kube, Mirror=true": {
			SourceKubeNS: "kube",
			Mirror:       true,
			ExpConsulNS:  "kube",
		},
		"SourceKubeNS=default, Mirror=true, Prefix=prefix": {
			SourceKubeNS: "default",
			Mirror:       true,
			MirrorPrefix: "prefix-",
			ExpConsulNS:  "prefix-default",
		},
	}
	for name, test := range cases {
		setup := struct {
			consulSvcName              string
			k8sObjects                 func() []runtime.Object
			initialConsulSvcs          []*api.AgentServiceRegistration
			expectedNumSvcInstances    int
			expectedConsulSvcInstances []*api.CatalogService
			expectedProxySvcInstances  []*api.CatalogService
			expectedAgentHealthChecks  []*api.AgentCheck
		}{
			consulSvcName: "service-created",
			k8sObjects: func() []runtime.Object {
				pod1 := createPodWithNamespace("pod1", test.SourceKubeNS, "1.2.3.4", true)
				pod2 := createPodWithNamespace("pod2", test.SourceKubeNS, "2.2.3.4", true)
				endpointWithTwoAddresses := &corev1.Endpoints{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "service-created",
						Namespace: test.SourceKubeNS,
					},
					Subsets: []corev1.EndpointSubset{
						{
							Addresses: []corev1.EndpointAddress{
								{
									IP:       "1.2.3.4",
									NodeName: &nodeName,
									TargetRef: &corev1.ObjectReference{
										Kind:      "Pod",
										Name:      "pod1",
										Namespace: test.SourceKubeNS,
									},
								},
								{
									IP:       "2.2.3.4",
									NodeName: &nodeName,
									TargetRef: &corev1.ObjectReference{
										Kind:      "Pod",
										Name:      "pod2",
										Namespace: test.SourceKubeNS,
									},
								},
							},
						},
					},
				}
				return []runtime.Object{pod1, pod2, endpointWithTwoAddresses}
			},
			initialConsulSvcs:       []*api.AgentServiceRegistration{},
			expectedNumSvcInstances: 2,
			expectedConsulSvcInstances: []*api.CatalogService{
				{
					ServiceID:      "pod1-service-created",
					ServiceName:    "service-created",
					ServiceAddress: "1.2.3.4",
					ServiceMeta:    map[string]string{MetaKeyPodName: "pod1", MetaKeyKubeServiceName: "service-created", MetaKeyKubeNS: test.SourceKubeNS},
					ServiceTags:    []string{},
					Namespace:      test.ExpConsulNS,
				},
				{
					ServiceID:      "pod2-service-created",
					ServiceName:    "service-created",
					ServiceAddress: "2.2.3.4",
					ServiceMeta:    map[string]string{MetaKeyPodName: "pod2", MetaKeyKubeServiceName: "service-created", MetaKeyKubeNS: test.SourceKubeNS},
					ServiceTags:    []string{},
					Namespace:      test.ExpConsulNS,
				},
			},
			expectedProxySvcInstances: []*api.CatalogService{
				{
					ServiceID:      "pod1-service-created-sidecar-proxy",
					ServiceName:    "service-created-sidecar-proxy",
					ServiceAddress: "1.2.3.4",
					ServicePort:    20000,
					ServiceProxy: &api.AgentServiceConnectProxyConfig{
						DestinationServiceName: "service-created",
						DestinationServiceID:   "pod1-service-created",
					},
					ServiceMeta: map[string]string{MetaKeyPodName: "pod1", MetaKeyKubeServiceName: "service-created", MetaKeyKubeNS: test.SourceKubeNS},
					ServiceTags: []string{},
					Namespace:   test.ExpConsulNS,
				},
				{
					ServiceID:      "pod2-service-created-sidecar-proxy",
					ServiceName:    "service-created-sidecar-proxy",
					ServiceAddress: "2.2.3.4",
					ServicePort:    20000,
					ServiceProxy: &api.AgentServiceConnectProxyConfig{
						DestinationServiceName: "service-created",
						DestinationServiceID:   "pod2-service-created",
					},
					ServiceMeta: map[string]string{MetaKeyPodName: "pod2", MetaKeyKubeServiceName: "service-created", MetaKeyKubeNS: test.SourceKubeNS},
					ServiceTags: []string{},
					Namespace:   test.ExpConsulNS,
				},
			},
			expectedAgentHealthChecks: []*api.AgentCheck{
				{
					CheckID:     fmt.Sprintf("%s/pod1-service-created/kubernetes-health-check", test.SourceKubeNS),
					ServiceName: "service-created",
					ServiceID:   "pod1-service-created",
					Name:        "Kubernetes Health Check",
					Status:      api.HealthPassing,
					Output:      kubernetesSuccessReasonMsg,
					Type:        ttl,
					Namespace:   test.ExpConsulNS,
				},
				{
					CheckID:     fmt.Sprintf("%s/pod2-service-created/kubernetes-health-check", test.SourceKubeNS),
					ServiceName: "service-created",
					ServiceID:   "pod2-service-created",
					Name:        "Kubernetes Health Check",
					Status:      api.HealthPassing,
					Output:      kubernetesSuccessReasonMsg,
					Type:        ttl,
					Namespace:   test.ExpConsulNS,
				},
			},
		}
		t.Run(name, func(t *testing.T) {
			// The agent pod needs to have the address 127.0.0.1 so when the
			// code gets the agent pods via the label component=client, and
			// makes requests against the agent API, it will actually hit the
			// test server we have on localhost.
			fakeClientPod := createPod("fake-consul-client", "127.0.0.1", false)
			fakeClientPod.Labels = map[string]string{"component": "client", "app": "consul", "release": "consul"}

			// Create fake k8s client.
			k8sObjects := append(setup.k8sObjects(), fakeClientPod)
			fakeClient := fake.NewClientBuilder().WithRuntimeObjects(k8sObjects...).Build()

			// Create test Consul server.
			consul, err := testutil.NewTestServerConfigT(t, func(c *testutil.TestServerConfig) {
				c.NodeName = nodeName
			})
			require.NoError(t, err)
			defer consul.Stop()
			consul.WaitForLeader(t)

			cfg := &api.Config{
				Address:   consul.HTTPAddr,
				Namespace: test.ExpConsulNS,
			}
			consulClient, err := api.NewClient(cfg)
			require.NoError(t, err)
			addr := strings.Split(consul.HTTPAddr, ":")
			consulPort := addr[1]

			_, err = namespaces.EnsureExists(consulClient, test.ExpConsulNS, "")
			require.NoError(t, err)

			// Register service and proxy in Consul.
			for _, svc := range setup.initialConsulSvcs {
				err = consulClient.Agent().ServiceRegister(svc)
				require.NoError(t, err)
			}

			// Create the endpoints controller.
			ep := &EndpointsController{
				Client:                     fakeClient,
				Log:                        logrtest.TestLogger{T: t},
				ConsulClient:               consulClient,
				ConsulPort:                 consulPort,
				ConsulScheme:               "http",
				AllowK8sNamespacesSet:      mapset.NewSetWith("*"),
				DenyK8sNamespacesSet:       mapset.NewSetWith(),
				ReleaseName:                "consul",
				ReleaseNamespace:           "default",
				ConsulClientCfg:            cfg,
				EnableConsulNamespaces:     true,
				ConsulDestinationNamespace: test.DestConsulNS,
				EnableNSMirroring:          test.Mirror,
				NSMirroringPrefix:          test.MirrorPrefix,
			}
			namespacedName := types.NamespacedName{
				Namespace: test.SourceKubeNS,
				Name:      "service-created",
			}

			resp, err := ep.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: namespacedName,
			})
			require.NoError(t, err)
			require.False(t, resp.Requeue)

			// After reconciliation, Consul should have the service with the correct number of instances.
			serviceInstances, _, err := consulClient.Catalog().Service(setup.consulSvcName, "", &api.QueryOptions{Namespace: test.ExpConsulNS})
			require.NoError(t, err)
			require.Len(t, serviceInstances, setup.expectedNumSvcInstances)
			for i, instance := range serviceInstances {
				require.Equal(t, setup.expectedConsulSvcInstances[i].ServiceID, instance.ServiceID)
				require.Equal(t, setup.expectedConsulSvcInstances[i].ServiceName, instance.ServiceName)
				require.Equal(t, setup.expectedConsulSvcInstances[i].ServiceAddress, instance.ServiceAddress)
				require.Equal(t, setup.expectedConsulSvcInstances[i].ServicePort, instance.ServicePort)
				require.Equal(t, setup.expectedConsulSvcInstances[i].ServiceMeta, instance.ServiceMeta)
				require.Equal(t, setup.expectedConsulSvcInstances[i].ServiceTags, instance.ServiceTags)
			}
			proxyServiceInstances, _, err := consulClient.Catalog().Service(fmt.Sprintf("%s-sidecar-proxy", setup.consulSvcName), "", &api.QueryOptions{
				Namespace: test.ExpConsulNS,
			})
			require.NoError(t, err)
			require.Len(t, proxyServiceInstances, setup.expectedNumSvcInstances)
			for i, instance := range proxyServiceInstances {
				require.Equal(t, setup.expectedProxySvcInstances[i].ServiceID, instance.ServiceID)
				require.Equal(t, setup.expectedProxySvcInstances[i].ServiceName, instance.ServiceName)
				require.Equal(t, setup.expectedProxySvcInstances[i].ServiceAddress, instance.ServiceAddress)
				require.Equal(t, setup.expectedProxySvcInstances[i].ServicePort, instance.ServicePort)
				require.Equal(t, setup.expectedProxySvcInstances[i].ServiceProxy, instance.ServiceProxy)
				require.Equal(t, setup.expectedProxySvcInstances[i].ServiceMeta, instance.ServiceMeta)
				require.Equal(t, setup.expectedProxySvcInstances[i].ServiceTags, instance.ServiceTags)
			}

			_, checkInfos, err := consulClient.Agent().AgentHealthServiceByName(fmt.Sprintf("%s-sidecar-proxy", setup.consulSvcName))
			expectedChecks := []string{"Proxy Public Listener", "Destination Alias"}
			require.NoError(t, err)
			require.Len(t, checkInfos, setup.expectedNumSvcInstances)
			for _, checkInfo := range checkInfos {
				checks := checkInfo.Checks
				require.Contains(t, expectedChecks, checks[0].Name)
				require.Contains(t, expectedChecks, checks[1].Name)
			}

			// Check that the Consul health check was created for the k8s pod.
			if setup.expectedAgentHealthChecks != nil {
				for i := range setup.expectedConsulSvcInstances {
					filter := fmt.Sprintf("CheckID == `%s`", setup.expectedAgentHealthChecks[i].CheckID)
					newChecks, _ := consulClient.Agent().Checks()
					for key, value := range newChecks {
						fmt.Printf("%s:%v\n", key, value)
					}
					check, err := consulClient.Agent().ChecksWithFilter(filter)
					require.NoError(t, err)
					require.EqualValues(t, 1, len(check))
					// Ignoring Namespace because the response from ENT includes it and OSS does not.
					var ignoredFields = []string{"Node", "Definition", "Namespace"}
					require.True(t, cmp.Equal(check[setup.expectedAgentHealthChecks[i].CheckID], setup.expectedAgentHealthChecks[i], cmpopts.IgnoreFields(api.AgentCheck{}, ignoredFields...)))
				}
			}
		})
	}
}

// Tests updating an Endpoints object when Consul namespaces are enabled.
//   - Tests updates via the register codepath:
//     - When an address in an Endpoint is updated, that the corresponding service instance in Consul is updated in the correct Consul namespace.
//     - When an address is added to an Endpoint, an additional service instance in Consul is registered in the correct Consul namespace.
//   - Tests updates via the deregister codepath:
//     - When an address is removed from an Endpoint, the corresponding service instance in Consul is deregistered.
//     - When an address is removed from an Endpoint *and there are no addresses left in the Endpoint*, the
//     corresponding service instance in Consul is deregistered.
// For the register and deregister codepath, this also tests that they work when the Consul service name is different
// from the K8s service name.
// This test covers EndpointsController.deregisterServiceOnAllAgents when services should be selectively deregistered
// since the map will not be nil. This test also runs each test with ACLs+TLS enabled and disabled, since it covers all the cases where a Consul client is created.
func TestReconcileUpdateEndpointWithNamespaces(t *testing.T) {
	t.Parallel()
	nodeName := "test-node"
	cases := map[string]struct {
		Mirror       bool
		MirrorPrefix string
		SourceKubeNS string
		DestConsulNS string
		ExpConsulNS  string
	}{
		"SourceKubeNS=default, DestConsulNS=default": {
			SourceKubeNS: "default",
			DestConsulNS: "default",
			ExpConsulNS:  "default",
		},
		"SourceKubeNS=kube, DestConsulNS=default": {
			SourceKubeNS: "kube",
			DestConsulNS: "default",
			ExpConsulNS:  "default",
		},
		"SourceKubeNS=default, DestConsulNS=other": {
			SourceKubeNS: "default",
			DestConsulNS: "other",
			ExpConsulNS:  "other",
		},
		"SourceKubeNS=kube, DestConsulNS=other": {
			SourceKubeNS: "kube",
			DestConsulNS: "other",
			ExpConsulNS:  "other",
		},
		"SourceKubeNS=default, Mirror=true": {
			SourceKubeNS: "default",
			Mirror:       true,
			ExpConsulNS:  "default",
		},
		"SourceKubeNS=kube, Mirror=true": {
			SourceKubeNS: "kube",
			Mirror:       true,
			ExpConsulNS:  "kube",
		},
		"SourceKubeNS=default, Mirror=true, Prefix=prefix": {
			SourceKubeNS: "default",
			Mirror:       true,
			MirrorPrefix: "prefix-",
			ExpConsulNS:  "prefix-default",
		},
	}
	for name, ts := range cases {
		cases := []struct {
			name                       string
			consulSvcName              string
			k8sObjects                 func() []runtime.Object
			initialConsulSvcs          []*api.AgentServiceRegistration
			expectedNumSvcInstances    int
			expectedConsulSvcInstances []*api.CatalogService
			expectedProxySvcInstances  []*api.CatalogService
		}{
			{
				name:          "Endpoints has an updated address (pod IP change).",
				consulSvcName: "service-updated",
				k8sObjects: func() []runtime.Object {
					pod1 := createPodWithNamespace("pod1", ts.SourceKubeNS, "4.4.4.4", true)
					endpoint := &corev1.Endpoints{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "service-updated",
							Namespace: ts.SourceKubeNS,
						},
						Subsets: []corev1.EndpointSubset{
							{
								Addresses: []corev1.EndpointAddress{
									{
										IP:       "4.4.4.4",
										NodeName: &nodeName,
										TargetRef: &corev1.ObjectReference{
											Kind:      "Pod",
											Name:      "pod1",
											Namespace: ts.SourceKubeNS,
										},
									},
								},
							},
						},
					}
					return []runtime.Object{pod1, endpoint}
				},
				initialConsulSvcs: []*api.AgentServiceRegistration{
					{
						ID:        "pod1-service-updated",
						Name:      "service-updated",
						Port:      80,
						Address:   "1.2.3.4",
						Namespace: ts.ExpConsulNS,
					},
					{
						Kind:    api.ServiceKindConnectProxy,
						ID:      "pod1-service-updated-sidecar-proxy",
						Name:    "service-updated-sidecar-proxy",
						Port:    20000,
						Address: "1.2.3.4",
						Proxy: &api.AgentServiceConnectProxyConfig{
							DestinationServiceName: "service-updated",
							DestinationServiceID:   "pod1-service-updated",
						},
						Namespace: ts.ExpConsulNS,
					},
				},
				expectedNumSvcInstances: 1,
				expectedConsulSvcInstances: []*api.CatalogService{
					{
						ServiceID:      "pod1-service-updated",
						ServiceAddress: "4.4.4.4",
						Namespace:      ts.ExpConsulNS,
					},
				},
				expectedProxySvcInstances: []*api.CatalogService{
					{
						ServiceID:      "pod1-service-updated-sidecar-proxy",
						ServiceAddress: "4.4.4.4",
						Namespace:      ts.ExpConsulNS,
					},
				},
			},
			{
				name:          "Different Consul service name: Endpoints has an updated address (pod IP change).",
				consulSvcName: "different-consul-svc-name",
				k8sObjects: func() []runtime.Object {
					pod1 := createPodWithNamespace("pod1", ts.SourceKubeNS, "4.4.4.4", true)
					pod1.Annotations[annotationService] = "different-consul-svc-name"
					endpoint := &corev1.Endpoints{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "service-updated",
							Namespace: ts.SourceKubeNS,
						},
						Subsets: []corev1.EndpointSubset{
							{
								Addresses: []corev1.EndpointAddress{
									{
										IP:       "4.4.4.4",
										NodeName: &nodeName,
										TargetRef: &corev1.ObjectReference{
											Kind:      "Pod",
											Name:      "pod1",
											Namespace: ts.SourceKubeNS,
										},
									},
								},
							},
						},
					}
					return []runtime.Object{pod1, endpoint}
				},
				initialConsulSvcs: []*api.AgentServiceRegistration{
					{
						ID:        "pod1-different-consul-svc-name",
						Name:      "different-consul-svc-name",
						Port:      80,
						Address:   "1.2.3.4",
						Namespace: ts.ExpConsulNS,
					},
					{
						Kind:    api.ServiceKindConnectProxy,
						ID:      "pod1-different-consul-svc-name-sidecar-proxy",
						Name:    "different-consul-svc-name-sidecar-proxy",
						Port:    20000,
						Address: "1.2.3.4",
						Proxy: &api.AgentServiceConnectProxyConfig{
							DestinationServiceName: "different-consul-svc-name",
							DestinationServiceID:   "pod1-different-consul-svc-name",
						},
						Namespace: ts.ExpConsulNS,
					},
				},
				expectedNumSvcInstances: 1,
				expectedConsulSvcInstances: []*api.CatalogService{
					{
						ServiceID:      "pod1-different-consul-svc-name",
						ServiceAddress: "4.4.4.4",
						Namespace:      ts.ExpConsulNS,
					},
				},
				expectedProxySvcInstances: []*api.CatalogService{
					{
						ServiceID:      "pod1-different-consul-svc-name-sidecar-proxy",
						ServiceAddress: "4.4.4.4",
						Namespace:      ts.ExpConsulNS,
					},
				},
			},
			{
				name:          "Endpoints has additional address not in Consul.",
				consulSvcName: "service-updated",
				k8sObjects: func() []runtime.Object {
					pod1 := createPodWithNamespace("pod1", ts.SourceKubeNS, "1.2.3.4", true)
					pod2 := createPodWithNamespace("pod2", ts.SourceKubeNS, "2.2.3.4", true)
					endpointWithTwoAddresses := &corev1.Endpoints{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "service-updated",
							Namespace: ts.SourceKubeNS,
						},
						Subsets: []corev1.EndpointSubset{
							{
								Addresses: []corev1.EndpointAddress{
									{
										IP:       "1.2.3.4",
										NodeName: &nodeName,
										TargetRef: &corev1.ObjectReference{
											Kind:      "Pod",
											Name:      "pod1",
											Namespace: ts.SourceKubeNS,
										},
									},
									{
										IP:       "2.2.3.4",
										NodeName: &nodeName,
										TargetRef: &corev1.ObjectReference{
											Kind:      "Pod",
											Name:      "pod2",
											Namespace: ts.SourceKubeNS,
										},
									},
								},
							},
						},
					}
					return []runtime.Object{pod1, pod2, endpointWithTwoAddresses}
				},
				initialConsulSvcs: []*api.AgentServiceRegistration{
					{
						ID:        "pod1-service-updated",
						Name:      "service-updated",
						Port:      80,
						Address:   "1.2.3.4",
						Namespace: ts.ExpConsulNS,
					},
					{
						Kind:    api.ServiceKindConnectProxy,
						ID:      "pod1-service-updated-sidecar-proxy",
						Name:    "service-updated-sidecar-proxy",
						Port:    20000,
						Address: "1.2.3.4",
						Proxy: &api.AgentServiceConnectProxyConfig{
							DestinationServiceName: "service-updated",
							DestinationServiceID:   "pod1-service-updated",
						},
						Namespace: ts.ExpConsulNS,
					},
				},
				expectedNumSvcInstances: 2,
				expectedConsulSvcInstances: []*api.CatalogService{
					{
						ServiceID:      "pod1-service-updated",
						ServiceAddress: "1.2.3.4",
						Namespace:      ts.ExpConsulNS,
					},
					{
						ServiceID:      "pod2-service-updated",
						ServiceAddress: "2.2.3.4",
						Namespace:      ts.ExpConsulNS,
					},
				},
				expectedProxySvcInstances: []*api.CatalogService{
					{
						ServiceID:      "pod1-service-updated-sidecar-proxy",
						ServiceAddress: "1.2.3.4",
						Namespace:      ts.ExpConsulNS,
					},
					{
						ServiceID:      "pod2-service-updated-sidecar-proxy",
						ServiceAddress: "2.2.3.4",
						Namespace:      ts.ExpConsulNS,
					},
				},
			},
			{
				name:          "Consul has instances that are not in the Endpoints addresses.",
				consulSvcName: "service-updated",
				k8sObjects: func() []runtime.Object {
					pod1 := createPodWithNamespace("pod1", ts.SourceKubeNS, "1.2.3.4", true)
					endpoint := &corev1.Endpoints{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "service-updated",
							Namespace: ts.SourceKubeNS,
						},
						Subsets: []corev1.EndpointSubset{
							{
								Addresses: []corev1.EndpointAddress{
									{
										IP:       "1.2.3.4",
										NodeName: &nodeName,
										TargetRef: &corev1.ObjectReference{
											Kind:      "Pod",
											Name:      "pod1",
											Namespace: ts.SourceKubeNS,
										},
									},
								},
							},
						},
					}
					return []runtime.Object{pod1, endpoint}
				},
				initialConsulSvcs: []*api.AgentServiceRegistration{
					{
						ID:        "pod1-service-updated",
						Name:      "service-updated",
						Port:      80,
						Address:   "1.2.3.4",
						Meta:      map[string]string{"k8s-service-name": "service-updated", "k8s-namespace": ts.SourceKubeNS},
						Namespace: ts.ExpConsulNS,
					},
					{
						Kind:    api.ServiceKindConnectProxy,
						ID:      "pod1-service-updated-sidecar-proxy",
						Name:    "service-updated-sidecar-proxy",
						Port:    20000,
						Address: "1.2.3.4",
						Proxy: &api.AgentServiceConnectProxyConfig{
							DestinationServiceName: "service-updated",
							DestinationServiceID:   "pod1-service-updated",
						},
						Meta:      map[string]string{"k8s-service-name": "service-updated", "k8s-namespace": ts.SourceKubeNS},
						Namespace: ts.ExpConsulNS,
					},
					{
						ID:        "pod2-service-updated",
						Name:      "service-updated",
						Port:      80,
						Address:   "2.2.3.4",
						Meta:      map[string]string{"k8s-service-name": "service-updated", "k8s-namespace": ts.SourceKubeNS},
						Namespace: ts.ExpConsulNS,
					},
					{
						Kind:    api.ServiceKindConnectProxy,
						ID:      "pod2-service-updated-sidecar-proxy",
						Name:    "service-updated-sidecar-proxy",
						Port:    20000,
						Address: "2.2.3.4",
						Proxy: &api.AgentServiceConnectProxyConfig{
							DestinationServiceName: "service-updated",
							DestinationServiceID:   "pod2-service-updated",
						},
						Meta:      map[string]string{"k8s-service-name": "service-updated", "k8s-namespace": ts.SourceKubeNS},
						Namespace: ts.ExpConsulNS,
					},
				},
				expectedNumSvcInstances: 1,
				expectedConsulSvcInstances: []*api.CatalogService{
					{
						ServiceID:      "pod1-service-updated",
						ServiceAddress: "1.2.3.4",
						Namespace:      ts.ExpConsulNS,
					},
				},
				expectedProxySvcInstances: []*api.CatalogService{
					{
						ServiceID:      "pod1-service-updated-sidecar-proxy",
						ServiceAddress: "1.2.3.4",
						Namespace:      ts.ExpConsulNS,
					},
				},
			},
			{
				name:          "Different Consul service name: Consul has instances that are not in the Endpoints addresses.",
				consulSvcName: "different-consul-svc-name",
				k8sObjects: func() []runtime.Object {
					pod1 := createPodWithNamespace("pod1", ts.SourceKubeNS, "1.2.3.4", true)
					pod1.Annotations[annotationService] = "different-consul-svc-name"
					endpoint := &corev1.Endpoints{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "service-updated",
							Namespace: ts.SourceKubeNS,
						},
						Subsets: []corev1.EndpointSubset{
							{
								Addresses: []corev1.EndpointAddress{
									{
										IP:       "1.2.3.4",
										NodeName: &nodeName,
										TargetRef: &corev1.ObjectReference{
											Kind:      "Pod",
											Name:      "pod1",
											Namespace: ts.SourceKubeNS,
										},
									},
								},
							},
						},
					}
					return []runtime.Object{pod1, endpoint}
				},
				initialConsulSvcs: []*api.AgentServiceRegistration{
					{
						ID:        "pod1-different-consul-svc-name",
						Name:      "different-consul-svc-name",
						Port:      80,
						Address:   "1.2.3.4",
						Meta:      map[string]string{"k8s-service-name": "service-updated", "k8s-namespace": ts.SourceKubeNS},
						Namespace: ts.ExpConsulNS,
					},
					{
						Kind:    api.ServiceKindConnectProxy,
						ID:      "pod1-different-consul-svc-name-sidecar-proxy",
						Name:    "different-consul-svc-name-sidecar-proxy",
						Port:    20000,
						Address: "1.2.3.4",
						Proxy: &api.AgentServiceConnectProxyConfig{
							DestinationServiceName: "different-consul-svc-name",
							DestinationServiceID:   "pod1-different-consul-svc-name",
						},
						Meta:      map[string]string{"k8s-service-name": "service-updated", "k8s-namespace": ts.SourceKubeNS},
						Namespace: ts.ExpConsulNS,
					},
					{
						ID:        "pod2-different-consul-svc-name",
						Name:      "different-consul-svc-name",
						Port:      80,
						Address:   "2.2.3.4",
						Meta:      map[string]string{"k8s-service-name": "service-updated", "k8s-namespace": ts.SourceKubeNS},
						Namespace: ts.ExpConsulNS,
					},
					{
						Kind:    api.ServiceKindConnectProxy,
						ID:      "pod2-different-consul-svc-name-sidecar-proxy",
						Name:    "different-consul-svc-name-sidecar-proxy",
						Port:    20000,
						Address: "2.2.3.4",
						Proxy: &api.AgentServiceConnectProxyConfig{
							DestinationServiceName: "different-consul-svc-name",
							DestinationServiceID:   "pod2-different-consul-svc-name",
						},
						Meta:      map[string]string{"k8s-service-name": "service-updated", "k8s-namespace": ts.SourceKubeNS},
						Namespace: ts.ExpConsulNS,
					},
				},
				expectedNumSvcInstances: 1,
				expectedConsulSvcInstances: []*api.CatalogService{
					{
						ServiceID:      "pod1-different-consul-svc-name",
						ServiceAddress: "1.2.3.4",
						Namespace:      ts.ExpConsulNS,
					},
				},
				expectedProxySvcInstances: []*api.CatalogService{
					{
						ServiceID:      "pod1-different-consul-svc-name-sidecar-proxy",
						ServiceAddress: "1.2.3.4",
						Namespace:      ts.ExpConsulNS,
					},
				},
			},
			{
				// When a k8s deployment is deleted but it's k8s service continues to exist, the endpoints has no addresses
				// and the instances should be deleted from Consul.
				name:          "Consul has instances that are not in the endpoints, and the endpoints has no addresses.",
				consulSvcName: "service-updated",
				k8sObjects: func() []runtime.Object {
					endpoint := &corev1.Endpoints{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "service-updated",
							Namespace: ts.SourceKubeNS,
						},
					}
					return []runtime.Object{endpoint}
				},
				initialConsulSvcs: []*api.AgentServiceRegistration{
					{
						ID:        "pod1-service-updated",
						Name:      "service-updated",
						Port:      80,
						Address:   "1.2.3.4",
						Meta:      map[string]string{"k8s-service-name": "service-updated", "k8s-namespace": ts.SourceKubeNS},
						Namespace: ts.ExpConsulNS,
					},
					{
						Kind:    api.ServiceKindConnectProxy,
						ID:      "pod1-service-updated-sidecar-proxy",
						Name:    "service-updated-sidecar-proxy",
						Port:    20000,
						Address: "1.2.3.4",
						Proxy: &api.AgentServiceConnectProxyConfig{
							DestinationServiceName: "service-updated",
							DestinationServiceID:   "pod1-service-updated",
						},
						Meta:      map[string]string{"k8s-service-name": "service-updated", "k8s-namespace": ts.SourceKubeNS},
						Namespace: ts.ExpConsulNS,
					},
					{
						ID:        "pod2-service-updated",
						Name:      "service-updated",
						Port:      80,
						Address:   "2.2.3.4",
						Meta:      map[string]string{"k8s-service-name": "service-updated", "k8s-namespace": ts.SourceKubeNS},
						Namespace: ts.ExpConsulNS,
					},
					{
						Kind:    api.ServiceKindConnectProxy,
						ID:      "pod2-service-updated-sidecar-proxy",
						Name:    "service-updated-sidecar-proxy",
						Port:    20000,
						Address: "2.2.3.4",
						Proxy: &api.AgentServiceConnectProxyConfig{
							DestinationServiceName: "service-updated",
							DestinationServiceID:   "pod2-service-updated",
						},
						Meta:      map[string]string{"k8s-service-name": "service-updated", "k8s-namespace": ts.SourceKubeNS},
						Namespace: ts.ExpConsulNS,
					},
				},
				expectedNumSvcInstances:    0,
				expectedConsulSvcInstances: []*api.CatalogService{},
				expectedProxySvcInstances:  []*api.CatalogService{},
			},
			{
				// With a different Consul service name, when a k8s deployment is deleted but it's k8s service continues to
				// exist, the endpoints has no addresses and the instances should be deleted from Consul.
				name:          "Different Consul service name: Consul has instances that are not in the endpoints, and the endpoints has no addresses.",
				consulSvcName: "different-consul-svc-name",
				k8sObjects: func() []runtime.Object {
					endpoint := &corev1.Endpoints{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "service-updated",
							Namespace: ts.SourceKubeNS,
						},
					}
					return []runtime.Object{endpoint}
				},
				initialConsulSvcs: []*api.AgentServiceRegistration{
					{
						ID:        "pod1-different-consul-svc-name",
						Name:      "different-consul-svc-name",
						Port:      80,
						Address:   "1.2.3.4",
						Meta:      map[string]string{"k8s-service-name": "service-updated", "k8s-namespace": ts.SourceKubeNS},
						Namespace: ts.ExpConsulNS,
					},
					{
						Kind:    api.ServiceKindConnectProxy,
						ID:      "pod1-different-consul-svc-name-sidecar-proxy",
						Name:    "different-consul-svc-name-sidecar-proxy",
						Port:    20000,
						Address: "1.2.3.4",
						Proxy: &api.AgentServiceConnectProxyConfig{
							DestinationServiceName: "different-consul-svc-name",
							DestinationServiceID:   "pod1-different-consul-svc-name",
						},
						Meta:      map[string]string{"k8s-service-name": "service-updated", "k8s-namespace": ts.SourceKubeNS},
						Namespace: ts.ExpConsulNS,
					},
					{
						ID:        "pod2-different-consul-svc-name",
						Name:      "different-consul-svc-name",
						Port:      80,
						Address:   "2.2.3.4",
						Meta:      map[string]string{"k8s-service-name": "service-updated", "k8s-namespace": ts.SourceKubeNS},
						Namespace: ts.ExpConsulNS,
					},
					{
						Kind:    api.ServiceKindConnectProxy,
						ID:      "pod2-different-consul-svc-name-sidecar-proxy",
						Name:    "different-consul-svc-name-sidecar-proxy",
						Port:    20000,
						Address: "2.2.3.4",
						Proxy: &api.AgentServiceConnectProxyConfig{
							DestinationServiceName: "different-consul-svc-name",
							DestinationServiceID:   "pod2-different-consul-svc-name",
						},
						Meta:      map[string]string{"k8s-service-name": "service-updated", "k8s-namespace": ts.SourceKubeNS},
						Namespace: ts.ExpConsulNS,
					},
				},
				expectedNumSvcInstances:    0,
				expectedConsulSvcInstances: []*api.CatalogService{},
				expectedProxySvcInstances:  []*api.CatalogService{},
			},
		}
		for _, secure := range []bool{true, false} {
			for _, tt := range cases {
				t.Run(fmt.Sprintf("%s: %s - secure: %v", name, tt.name, secure), func(t *testing.T) {
					// The agent pod needs to have the address 127.0.0.1 so when the
					// code gets the agent pods via the label component=client, and
					// makes requests against the agent API, it will actually hit the
					// test server we have on localhost.
					fakeClientPod := createPod("fake-consul-client", "127.0.0.1", false)
					fakeClientPod.Labels = map[string]string{"component": "client", "app": "consul", "release": "consul"}

					// Create fake k8s client.
					k8sObjects := append(tt.k8sObjects(), fakeClientPod)
					fakeClient := fake.NewClientBuilder().WithRuntimeObjects(k8sObjects...).Build()

					masterToken := "b78d37c7-0ca7-5f4d-99ee-6d9975ce4586"
					caFile, certFile, keyFile := common.GenerateServerCerts(t)
					// Create test consul server, with ACLs+TLS if necessary.
					consul, err := testutil.NewTestServerConfigT(t, func(c *testutil.TestServerConfig) {
						if secure {
							c.ACL.Enabled = true
							c.ACL.DefaultPolicy = "deny"
							c.ACL.Tokens.Master = masterToken
							c.CAFile = caFile
							c.CertFile = certFile
							c.KeyFile = keyFile
						}
						c.NodeName = nodeName
					})
					require.NoError(t, err)
					defer consul.Stop()
					consul.WaitForSerfCheck(t)

					cfg := &api.Config{
						Scheme:    "http",
						Address:   consul.HTTPAddr,
						Namespace: ts.ExpConsulNS,
					}
					if secure {
						cfg.Address = consul.HTTPSAddr
						cfg.Scheme = "https"
						cfg.TLSConfig = api.TLSConfig{
							CAFile: caFile,
						}
						cfg.Token = masterToken
					}
					consulClient, err := api.NewClient(cfg)
					require.NoError(t, err)
					addr := strings.Split(cfg.Address, ":")
					consulPort := addr[1]

					_, err = namespaces.EnsureExists(consulClient, ts.ExpConsulNS, "")
					require.NoError(t, err)

					// Register service and proxy in Consul.
					for _, svc := range tt.initialConsulSvcs {
						err = consulClient.Agent().ServiceRegister(svc)
						require.NoError(t, err)
					}

					// Create the endpoints controller.
					ep := &EndpointsController{
						Client:                     fakeClient,
						Log:                        logrtest.TestLogger{T: t},
						ConsulClient:               consulClient,
						ConsulPort:                 consulPort,
						ConsulScheme:               cfg.Scheme,
						AllowK8sNamespacesSet:      mapset.NewSetWith("*"),
						DenyK8sNamespacesSet:       mapset.NewSetWith(),
						ReleaseName:                "consul",
						ReleaseNamespace:           "default",
						ConsulClientCfg:            cfg,
						EnableConsulNamespaces:     true,
						EnableNSMirroring:          ts.Mirror,
						NSMirroringPrefix:          ts.MirrorPrefix,
						ConsulDestinationNamespace: ts.DestConsulNS,
					}
					namespacedName := types.NamespacedName{
						Namespace: ts.SourceKubeNS,
						Name:      "service-updated",
					}

					resp, err := ep.Reconcile(context.Background(), ctrl.Request{
						NamespacedName: namespacedName,
					})
					require.NoError(t, err)
					require.False(t, resp.Requeue)

					// After reconciliation, Consul should have service-updated with the correct number of instances.
					serviceInstances, _, err := consulClient.Catalog().Service(tt.consulSvcName, "", &api.QueryOptions{Namespace: ts.ExpConsulNS})
					require.NoError(t, err)
					require.Len(t, serviceInstances, tt.expectedNumSvcInstances)
					for i, instance := range serviceInstances {
						require.Equal(t, tt.expectedConsulSvcInstances[i].ServiceID, instance.ServiceID)
						require.Equal(t, tt.expectedConsulSvcInstances[i].ServiceAddress, instance.ServiceAddress)
					}
					proxyServiceInstances, _, err := consulClient.Catalog().Service(fmt.Sprintf("%s-sidecar-proxy", tt.consulSvcName), "", &api.QueryOptions{Namespace: ts.ExpConsulNS})
					require.NoError(t, err)
					require.Len(t, proxyServiceInstances, tt.expectedNumSvcInstances)
					for i, instance := range proxyServiceInstances {
						require.Equal(t, tt.expectedProxySvcInstances[i].ServiceID, instance.ServiceID)
						require.Equal(t, tt.expectedProxySvcInstances[i].ServiceAddress, instance.ServiceAddress)
					}
				})
			}
		}
	}
}

// Tests deleting an Endpoints object, with and without matching Consul and K8s service names when Consul namespaces are enabled.
// This test covers EndpointsController.deregisterServiceOnAllAgents when the map is nil (not selectively deregistered).
func TestReconcileDeleteEndpointWithNamespaces(t *testing.T) {
	t.Parallel()
	nodeName := "test-node"
	cases := map[string]struct {
		Mirror       bool
		MirrorPrefix string
		SourceKubeNS string
		DestConsulNS string
		ExpConsulNS  string
	}{
		"SourceKubeNS=default, DestConsulNS=default": {
			SourceKubeNS: "default",
			DestConsulNS: "default",
			ExpConsulNS:  "default",
		},
		"SourceKubeNS=kube, DestConsulNS=default": {
			SourceKubeNS: "kube",
			DestConsulNS: "default",
			ExpConsulNS:  "default",
		},
		"SourceKubeNS=default, DestConsulNS=other": {
			SourceKubeNS: "default",
			DestConsulNS: "other",
			ExpConsulNS:  "other",
		},
		"SourceKubeNS=kube, DestConsulNS=other": {
			SourceKubeNS: "kube",
			DestConsulNS: "other",
			ExpConsulNS:  "other",
		},
		"SourceKubeNS=default, Mirror=true": {
			SourceKubeNS: "default",
			Mirror:       true,
			ExpConsulNS:  "default",
		},
		"SourceKubeNS=kube, Mirror=true": {
			SourceKubeNS: "kube",
			Mirror:       true,
			ExpConsulNS:  "kube",
		},
		"SourceKubeNS=default, Mirror=true, Prefix=prefix": {
			SourceKubeNS: "default",
			Mirror:       true,
			MirrorPrefix: "prefix-",
			ExpConsulNS:  "prefix-default",
		},
	}
	for name, ts := range cases {
		cases := []struct {
			name              string
			consulSvcName     string
			initialConsulSvcs []*api.AgentServiceRegistration
		}{
			{
				name:          "Consul service name matches K8s service name",
				consulSvcName: "service-deleted",
				initialConsulSvcs: []*api.AgentServiceRegistration{
					{
						ID:        "pod1-service-deleted",
						Name:      "service-deleted",
						Port:      80,
						Address:   "1.2.3.4",
						Meta:      map[string]string{"k8s-service-name": "service-deleted", "k8s-namespace": ts.SourceKubeNS},
						Namespace: ts.ExpConsulNS,
					},
					{
						Kind:    api.ServiceKindConnectProxy,
						ID:      "pod1-service-deleted-sidecar-proxy",
						Name:    "service-deleted-sidecar-proxy",
						Port:    20000,
						Address: "1.2.3.4",
						Proxy: &api.AgentServiceConnectProxyConfig{
							DestinationServiceName: "service-deleted",
							DestinationServiceID:   "pod1-service-deleted",
						},
						Meta:      map[string]string{"k8s-service-name": "service-deleted", "k8s-namespace": ts.SourceKubeNS},
						Namespace: ts.ExpConsulNS,
					},
				},
			},
			{
				name:          "Consul service name does not match K8s service name",
				consulSvcName: "different-consul-svc-name",
				initialConsulSvcs: []*api.AgentServiceRegistration{
					{
						ID:        "pod1-different-consul-svc-name",
						Name:      "different-consul-svc-name",
						Port:      80,
						Address:   "1.2.3.4",
						Meta:      map[string]string{"k8s-service-name": "service-deleted", "k8s-namespace": ts.SourceKubeNS},
						Namespace: ts.ExpConsulNS,
					},
					{
						Kind:    api.ServiceKindConnectProxy,
						ID:      "pod1-different-consul-svc-name-sidecar-proxy",
						Name:    "different-consul-svc-name-sidecar-proxy",
						Port:    20000,
						Address: "1.2.3.4",
						Proxy: &api.AgentServiceConnectProxyConfig{
							DestinationServiceName: "different-consul-svc-name",
							DestinationServiceID:   "pod1-different-consul-svc-name",
						},
						Meta:      map[string]string{"k8s-service-name": "service-deleted", "k8s-namespace": ts.SourceKubeNS},
						Namespace: ts.ExpConsulNS,
					},
				},
			},
		}
		for _, tt := range cases {
			t.Run(fmt.Sprintf("%s:%s", name, tt.name), func(t *testing.T) {
				// The agent pod needs to have the address 127.0.0.1 so when the
				// code gets the agent pods via the label component=client, and
				// makes requests against the agent API, it will actually hit the
				// test server we have on localhost.
				fakeClientPod := createPod("fake-consul-client", "127.0.0.1", false)
				fakeClientPod.Labels = map[string]string{"component": "client", "app": "consul", "release": "consul"}

				// Create fake k8s client.
				fakeClient := fake.NewClientBuilder().WithRuntimeObjects(fakeClientPod).Build()

				// Create test Consul server.
				consul, err := testutil.NewTestServerConfigT(t, func(c *testutil.TestServerConfig) {
					c.NodeName = nodeName
				})
				require.NoError(t, err)
				defer consul.Stop()

				consul.WaitForLeader(t)
				cfg := &api.Config{
					Address:   consul.HTTPAddr,
					Namespace: ts.ExpConsulNS,
				}
				consulClient, err := api.NewClient(cfg)
				require.NoError(t, err)
				addr := strings.Split(consul.HTTPAddr, ":")
				consulPort := addr[1]

				_, err = namespaces.EnsureExists(consulClient, ts.ExpConsulNS, "")
				require.NoError(t, err)

				// Register service and proxy in consul.
				for _, svc := range tt.initialConsulSvcs {
					err = consulClient.Agent().ServiceRegister(svc)
					require.NoError(t, err)
				}

				// Create the endpoints controller.
				ep := &EndpointsController{
					Client:                     fakeClient,
					Log:                        logrtest.TestLogger{T: t},
					ConsulClient:               consulClient,
					ConsulPort:                 consulPort,
					ConsulScheme:               "http",
					AllowK8sNamespacesSet:      mapset.NewSetWith("*"),
					DenyK8sNamespacesSet:       mapset.NewSetWith(),
					ReleaseName:                "consul",
					ReleaseNamespace:           "default",
					ConsulClientCfg:            cfg,
					EnableConsulNamespaces:     true,
					EnableNSMirroring:          ts.Mirror,
					NSMirroringPrefix:          ts.MirrorPrefix,
					ConsulDestinationNamespace: ts.DestConsulNS,
				}

				// Set up the Endpoint that will be reconciled, and reconcile.
				namespacedName := types.NamespacedName{
					Namespace: ts.SourceKubeNS,
					Name:      "service-deleted",
				}
				resp, err := ep.Reconcile(context.Background(), ctrl.Request{
					NamespacedName: namespacedName,
				})
				require.NoError(t, err)
				require.False(t, resp.Requeue)

				// After reconciliation, Consul should not have any instances of service-deleted.
				serviceInstances, _, err := consulClient.Catalog().Service(tt.consulSvcName, "", &api.QueryOptions{Namespace: ts.ExpConsulNS})
				require.NoError(t, err)
				require.Empty(t, serviceInstances)
				proxyServiceInstances, _, err := consulClient.Catalog().Service(fmt.Sprintf("%s-sidecar-proxy", tt.consulSvcName), "", &api.QueryOptions{Namespace: ts.ExpConsulNS})
				require.NoError(t, err)
				require.Empty(t, proxyServiceInstances)

			})
		}
	}
}

func createPodWithNamespace(name, namespace, ip string, inject bool) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Labels:      map[string]string{},
			Annotations: map[string]string{},
		},
		Status: corev1.PodStatus{
			PodIP:  ip,
			HostIP: "127.0.0.1",
			Phase:  corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
	if inject {
		pod.Labels[keyInjectStatus] = injected
		pod.Annotations[keyInjectStatus] = injected
	}
	return pod

}
