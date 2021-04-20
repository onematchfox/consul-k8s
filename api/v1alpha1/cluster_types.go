package v1alpha1

import (
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/hashicorp/consul-k8s/api/common"
	capi "github.com/hashicorp/consul/api"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ClusterKubeKind = "cluster"
)

func init() {
	SchemeBuilder.Register(&Cluster{}, &ClusterList{})
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Cluster is the Schema for the clusters API
// +kubebuilder:printcolumn:name="Synced",type="string",JSONPath=".status.conditions[?(@.type==\"Synced\")].status",description="The sync status of the resource with Consul"
// +kubebuilder:printcolumn:name="Last Synced",type="date",JSONPath=".status.lastSyncedTime",description="The last successful synced time of the resource with Consul"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description="The age of the resource"
type Cluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterSpec `json:"spec,omitempty"`
	Status `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ClusterList contains a list of Cluster
type ClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Cluster `json:"items"`
}

// ClusterSpec defines the desired state of Cluster
type ClusterSpec struct {
	TransparentProxy TransparentProxyClusterConfig `json:"transparentProxy,omitempty"`
}

type TransparentProxyClusterConfig struct {
	CatalogDestinationsOnly bool `json:"catalogDestinationsOnly,omitempty"`
}

func (in *TransparentProxyClusterConfig) toConsul() capi.TransparentProxyClusterConfig {
	return capi.TransparentProxyClusterConfig{CatalogDestinationsOnly: in.CatalogDestinationsOnly}
}

func (in *Cluster) GetObjectMeta() metav1.ObjectMeta {
	return in.ObjectMeta
}

func (in *Cluster) AddFinalizer(name string) {
	in.ObjectMeta.Finalizers = append(in.Finalizers(), name)
}

func (in *Cluster) RemoveFinalizer(name string) {
	var newFinalizers []string
	for _, oldF := range in.Finalizers() {
		if oldF != name {
			newFinalizers = append(newFinalizers, oldF)
		}
	}
	in.ObjectMeta.Finalizers = newFinalizers

}

func (in *Cluster) Finalizers() []string {
	return in.ObjectMeta.Finalizers
}

func (in *Cluster) ConsulKind() string {
	return capi.ClusterConfig
}

func (in *Cluster) ConsulMirroringNS() string {
	return common.DefaultConsulNamespace
}

func (in *Cluster) KubeKind() string {
	return ClusterKubeKind
}

func (in *Cluster) SyncedCondition() (status corev1.ConditionStatus, reason, message string) {
	cond := in.Status.GetCondition(ConditionSynced)
	if cond == nil {
		return corev1.ConditionUnknown, "", ""
	}
	return cond.Status, cond.Reason, cond.Message
}

func (in *Cluster) SyncedConditionStatus() corev1.ConditionStatus {
	cond := in.Status.GetCondition(ConditionSynced)
	if cond == nil {
		return corev1.ConditionUnknown
	}
	return cond.Status
}

func (in *Cluster) ConsulName() string {
	return in.ObjectMeta.Name
}

func (in *Cluster) ConsulGlobalResource() bool {
	return true
}

func (in *Cluster) KubernetesName() string {
	return in.ObjectMeta.Name
}

func (in *Cluster) SetSyncedCondition(status corev1.ConditionStatus, reason string, message string) {
	in.Status.Conditions = Conditions{
		{
			Type:               ConditionSynced,
			Status:             status,
			LastTransitionTime: metav1.Now(),
			Reason:             reason,
			Message:            message,
		},
	}
}

func (in *Cluster) SetLastSyncedTime(time *metav1.Time) {
	in.Status.LastSyncedTime = time
}

func (in *Cluster) ToConsul(datacenter string) capi.ConfigEntry {
	return &capi.ClusterConfigEntry{
		Kind:             in.ConsulKind(),
		Name:             in.ConsulName(),
		TransparentProxy: in.Spec.TransparentProxy.toConsul(),
		Meta:             meta(datacenter),
	}
}

func (in *Cluster) MatchesConsul(candidate capi.ConfigEntry) bool {
	configEntry, ok := candidate.(*capi.ClusterConfigEntry)
	if !ok {
		return false
	}
	// No datacenter is passed to ToConsul as we ignore the Meta field when checking for equality.
	return cmp.Equal(in.ToConsul(""), configEntry, cmpopts.IgnoreFields(capi.ClusterConfigEntry{}, "Namespace", "Meta", "ModifyIndex", "CreateIndex"), cmpopts.IgnoreUnexported(), cmpopts.EquateEmpty())
}

func (in *Cluster) Validate(_ bool) error {
	return nil
}

// DefaultNamespaceFields has no behaviour here as proxy-defaults have no namespace specific fields.
func (in *Cluster) DefaultNamespaceFields(_ bool, _ string, _ bool, _ string) {
	return
}
