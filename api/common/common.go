// Package common holds code that isn't tied to a particular CRD version or type.
package common

const (
	ServiceDefaults    string = "servicedefaults"
	ProxyDefaults      string = "proxydefaults"
	ServiceResolver    string = "serviceresolver"
	ServiceRouter      string = "servicerouter"
	ServiceSplitter    string = "servicesplitter"
	ServiceIntentions  string = "serviceintentions"
	IngressGateway     string = "ingressgateway"
	TerminatingGateway string = "terminatinggateway"

	Global                 string = "global"
	Cluster                string = "cluster"
	DefaultConsulNamespace string = "default"
	WildcardNamespace      string = "*"

	SourceKey        string = "external-source"
	DatacenterKey    string = "consul.hashicorp.com/source-datacenter"
	MigrateEntryKey  string = "consul.hashicorp.com/migrate-entry"
	MigrateEntryTrue string = "true"
	SourceValue      string = "kubernetes"
)
