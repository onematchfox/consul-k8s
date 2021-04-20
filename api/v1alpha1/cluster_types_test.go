package v1alpha1

import (
	"testing"
	"time"

	"github.com/hashicorp/consul-k8s/api/common"
	capi "github.com/hashicorp/consul/api"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Test MatchesConsul for cases that should return true.
func TestCluster_MatchesConsul(t *testing.T) {
	cases := map[string]struct {
		Ours    Cluster
		Theirs  capi.ConfigEntry
		Matches bool
	}{
		"empty fields matches": {
			Ours: Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: common.Cluster,
				},
				Spec: ClusterSpec{},
			},
			Theirs: &capi.ClusterConfigEntry{
				Name:        common.Cluster,
				Kind:        capi.ClusterConfig,
				Namespace:   "default",
				CreateIndex: 1,
				ModifyIndex: 2,
				Meta: map[string]string{
					common.SourceKey:     common.SourceValue,
					common.DatacenterKey: "datacenter",
				},
			},
			Matches: true,
		},
		"all fields set matches": {
			Ours: Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: common.Cluster,
				},
				Spec: ClusterSpec{
					TransparentProxy: TransparentProxyClusterConfig{
						CatalogDestinationsOnly: true,
					},
				},
			},
			Theirs: &capi.ClusterConfigEntry{
				Kind: capi.ClusterConfig,
				Name: common.Cluster,
				TransparentProxy: capi.TransparentProxyClusterConfig{
					CatalogDestinationsOnly: true,
				},
				CreateIndex: 1,
				ModifyIndex: 2,
				Meta: map[string]string{
					common.SourceKey:     common.SourceValue,
					common.DatacenterKey: "datacenter",
				},
			},
			Matches: true,
		},
		"mismatched types does not match": {
			Ours: Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: common.Cluster,
				},
				Spec: ClusterSpec{},
			},
			Theirs: &capi.ServiceConfigEntry{
				Name: common.Cluster,
				Kind: capi.ClusterConfig,
			},
			Matches: false,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, c.Matches, c.Ours.MatchesConsul(c.Theirs))
		})
	}
}

func TestCluster_ToConsul(t *testing.T) {
	cases := map[string]struct {
		Ours Cluster
		Exp  *capi.ClusterConfigEntry
	}{
		"empty fields": {
			Ours: Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "name",
				},
				Spec: ClusterSpec{},
			},
			Exp: &capi.ClusterConfigEntry{
				Name: "name",
				Kind: capi.ClusterConfig,
				Meta: map[string]string{
					common.SourceKey:     common.SourceValue,
					common.DatacenterKey: "datacenter",
				},
			},
		},
		"every field set": {
			Ours: Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "name",
				},
				Spec: ClusterSpec{
					TransparentProxy: TransparentProxyClusterConfig{
						CatalogDestinationsOnly: true,
					},
				},
			},
			Exp: &capi.ClusterConfigEntry{
				Kind: capi.ClusterConfig,
				Name: "name",
				TransparentProxy: capi.TransparentProxyClusterConfig{
					CatalogDestinationsOnly: true,
				},
				Namespace: "",
				Meta: map[string]string{
					common.SourceKey:     common.SourceValue,
					common.DatacenterKey: "datacenter",
				},
			},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			act := c.Ours.ToConsul("datacenter")
			cluster, ok := act.(*capi.ClusterConfigEntry)
			require.True(t, ok, "could not cast")
			require.Equal(t, c.Exp, cluster)
		})
	}
}

func TestCluster_AddFinalizer(t *testing.T) {
	cluster := &Cluster{}
	cluster.AddFinalizer("finalizer")
	require.Equal(t, []string{"finalizer"}, cluster.ObjectMeta.Finalizers)
}

func TestCluster_RemoveFinalizer(t *testing.T) {
	cluster := &Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Finalizers: []string{"f1", "f2"},
		},
	}
	cluster.RemoveFinalizer("f1")
	require.Equal(t, []string{"f2"}, cluster.ObjectMeta.Finalizers)
}

func TestCluster_SetSyncedCondition(t *testing.T) {
	cluster := &Cluster{}
	cluster.SetSyncedCondition(corev1.ConditionTrue, "reason", "message")

	require.Equal(t, corev1.ConditionTrue, cluster.Status.Conditions[0].Status)
	require.Equal(t, "reason", cluster.Status.Conditions[0].Reason)
	require.Equal(t, "message", cluster.Status.Conditions[0].Message)
	now := metav1.Now()
	require.True(t, cluster.Status.Conditions[0].LastTransitionTime.Before(&now))
}

func TestCluster_SetLastSyncedTime(t *testing.T) {
	cluster := &Cluster{}
	syncedTime := metav1.NewTime(time.Now())
	cluster.SetLastSyncedTime(&syncedTime)

	require.Equal(t, &syncedTime, cluster.Status.LastSyncedTime)
}

func TestCluster_GetSyncedConditionStatus(t *testing.T) {
	cases := []corev1.ConditionStatus{
		corev1.ConditionUnknown,
		corev1.ConditionFalse,
		corev1.ConditionTrue,
	}
	for _, status := range cases {
		t.Run(string(status), func(t *testing.T) {
			cluster := &Cluster{
				Status: Status{
					Conditions: []Condition{{
						Type:   ConditionSynced,
						Status: status,
					}},
				},
			}

			require.Equal(t, status, cluster.SyncedConditionStatus())
		})
	}
}

func TestCluster_GetConditionWhenStatusNil(t *testing.T) {
	require.Nil(t, (&Cluster{}).GetCondition(ConditionSynced))
}

func TestCluster_SyncedConditionStatusWhenStatusNil(t *testing.T) {
	require.Equal(t, corev1.ConditionUnknown, (&Cluster{}).SyncedConditionStatus())
}

func TestCluster_SyncedConditionWhenStatusNil(t *testing.T) {
	status, reason, message := (&Cluster{}).SyncedCondition()
	require.Equal(t, corev1.ConditionUnknown, status)
	require.Equal(t, "", reason)
	require.Equal(t, "", message)
}

func TestCluster_ConsulKind(t *testing.T) {
	require.Equal(t, capi.ClusterConfig, (&Cluster{}).ConsulKind())
}

func TestCluster_KubeKind(t *testing.T) {
	require.Equal(t, "cluster", (&Cluster{}).KubeKind())
}

func TestCluster_ConsulName(t *testing.T) {
	require.Equal(t, "foo", (&Cluster{ObjectMeta: metav1.ObjectMeta{Name: "foo"}}).ConsulName())
}

func TestCluster_KubernetesName(t *testing.T) {
	require.Equal(t, "foo", (&Cluster{ObjectMeta: metav1.ObjectMeta{Name: "foo"}}).KubernetesName())
}

func TestCluster_ConsulNamespace(t *testing.T) {
	require.Equal(t, common.DefaultConsulNamespace, (&Cluster{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "bar"}}).ConsulMirroringNS())
}

func TestCluster_ConsulGlobalResource(t *testing.T) {
	require.True(t, (&Cluster{}).ConsulGlobalResource())
}

func TestCluster_ObjectMeta(t *testing.T) {
	meta := metav1.ObjectMeta{
		Name:      "name",
		Namespace: "namespace",
	}
	cluster := &Cluster{
		ObjectMeta: meta,
	}
	require.Equal(t, meta, cluster.GetObjectMeta())
}
