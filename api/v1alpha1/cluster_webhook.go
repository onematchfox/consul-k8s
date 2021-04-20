package v1alpha1

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-logr/logr"
	"github.com/hashicorp/consul-k8s/api/common"
	capi "github.com/hashicorp/consul/api"
	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:object:generate=false

type ClusterWebhook struct {
	client.Client
	ConsulClient           *capi.Client
	Logger                 logr.Logger
	decoder                *admission.Decoder
	EnableConsulNamespaces bool
	EnableNSMirroring      bool
}

// NOTE: The path value in the below line is the path to the webhook.
// If it is updated, run code-gen, update subcommand/controller/command.go
// and the consul-helm value for the path to the webhook.
//
// NOTE: The below line cannot be combined with any other comment. If it is
// it will break the code generation.
//
// +kubebuilder:webhook:verbs=create;update,path=/mutate-v1alpha1-cluster,mutating=true,failurePolicy=fail,groups=consul.hashicorp.com,resources=cluster,versions=v1alpha1,name=mutate-cluster.consul.hashicorp.com,sideEffects=None,admissionReviewVersions=v1beta1;v1

func (v *ClusterWebhook) Handle(ctx context.Context, req admission.Request) admission.Response {
	var cluster Cluster
	var clusterList ClusterList
	err := v.decoder.Decode(req, &cluster)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	if req.Operation == admissionv1.Create {
		v.Logger.Info("validate create", "name", cluster.KubernetesName())

		if cluster.KubernetesName() != common.Cluster {
			return admission.Errored(http.StatusBadRequest,
				fmt.Errorf(`%s resource name must be "%s"`,
					cluster.KubeKind(), common.Cluster))
		}

		if err := v.Client.List(ctx, &clusterList); err != nil {
			return admission.Errored(http.StatusInternalServerError, err)
		}

		if len(clusterList.Items) > 0 {
			return admission.Errored(http.StatusBadRequest,
				fmt.Errorf("%s resource already defined - only one cluster entry is supported",
					cluster.KubeKind()))
		}
	}

	return admission.Allowed(fmt.Sprintf("valid %s request", cluster.KubeKind()))
}

func (v *ClusterWebhook) InjectDecoder(d *admission.Decoder) error {
	v.decoder = d
	return nil
}
