// Copyright © 2019 Banzai Cloud
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cluster

import (
	pConfig "github.com/banzaicloud/pipeline/config"
	"github.com/banzaicloud/pipeline/internal/istio"
	"github.com/banzaicloud/pipeline/pkg/cluster"
	pkgHelm "github.com/banzaicloud/pipeline/pkg/helm"
	"github.com/banzaicloud/pipeline/pkg/k8sclient"
	"github.com/ghodss/yaml"
	"github.com/goph/emperror"
	"github.com/spf13/viper"
)

// InstallServiceMeshParams describes InstallServiceMesh posthook params
type InstallServiceMeshParams struct {
	// AutoSidecarInjectNamespaces list of namespaces that will be labelled with istio-injection=enabled
	AutoSidecarInjectNamespaces []string `json:"autoSidecarInjectNamespaces,omitempty"`
	// BypassEgressTraffic prevents Envoy sidecars from intercepting external requests
	BypassEgressTraffic bool `json:"bypassEgressTraffic,omitempty"`
	// EnableMtls signals if mutual TLS is enabled in the service mesh
	EnableMtls bool `json:"mtls,omitempty"`
}

// InstallServiceMesh is a posthook for installing Istio on a cluster
func InstallServiceMesh(cluster CommonCluster, param cluster.PostHookParam) error {
	var params InstallServiceMeshParams
	err := castToPostHookParam(&param, &params)
	if err != nil {
		return emperror.Wrap(err, "failed to cast posthook param")
	}

	log.Infof("istio params: %#v", params)

	config := istio.Config{
		Global: istio.Global{
			Mtls: istio.MTLS{
				Enabled: params.EnableMtls,
			},
		},
	}

	if params.BypassEgressTraffic {
		ipRanges, err := cluster.GetK8sIpv4Cidrs()
		if err != nil {
			log.Warnf("couldn't set included IP ranges in Envoy config, external requests will be intercepted")
		} else {
			config.Global.Proxy = istio.Proxy{
				IncludeIPRanges: ipRanges.PodIPRange + "," + ipRanges.ServiceClusterIPRange,
			}
		}
	}

	overrideValues, err := yaml.Marshal(config)
	if err != nil {
		return emperror.Wrap(err, "failed to marshal yaml values")
	}

	err = installDeployment(cluster, istio.Namespace, pkgHelm.BanzaiRepository+"/istio", "istio", overrideValues, viper.GetString(pConfig.IstioChartVersion), false)
	if err != nil {
		return emperror.Wrap(err, "installing Istio failed")
	}

	kubeConfig, err := cluster.GetK8sConfig()
	if err != nil {
		return emperror.Wrap(err, "failed to get kubeconfig")
	}

	client, err := k8sclient.NewClientFromKubeConfig(kubeConfig)
	if err != nil {
		return emperror.Wrap(err, "failed to create client from kubeconfig")
	}

	err = istio.LabelNamespaces(log, client, params.AutoSidecarInjectNamespaces)
	if err != nil {
		return emperror.Wrap(err, "failed to label namespace")
	}

	if cluster.GetMonitoring() {
		err = istio.AddPrometheusTargets(log, client)
		if err != nil {
			return emperror.Wrap(err, "failed to add prometheus targets")
		}
		err = istio.AddGrafanaDashboards(log, client)
		if err != nil {
			return emperror.Wrap(err, "failed to add grafana dashboards")
		}
	}

	cluster.SetServiceMesh(true)
	return nil
}
