/*
Copyright 2021 Antrea Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package common

import corev1 "k8s.io/api/core/v1"

const (
	AntreaMCServiceAnnotation = "multicluster.antrea.io/imported-service"
	AntreaMCACNPAnnotation    = "multicluster.antrea.io/imported-acnp"
	GatewayAnnotation         = "multicluster.antrea.io/gateway"
	GatewayIPAnnotation       = "multicluster.antrea.io/gateway-ip"

	AntreaMCSPrefix                = "antrea-mc-"
	ServiceKind                    = "Service"
	EndpointsKind                  = "Endpoints"
	AntreaClusterNetworkPolicyKind = "AntreaClusterNetworkPolicy"
	ServiceImportKind              = "ServiceImport"
	ClusterInfoKind                = "ClusterInfo"

	SourceName      = "sourceName"
	SourceNamespace = "sourceNamespace"
	SourceClusterID = "sourceClusterID"
	SourceKind      = "sourceKind"

	DefaultWorkerCount = 5

	ResourceExportFinalizer = "resourceexport.finalizers.antrea.io"
)

// TODO: Use NamespacedName stringer method instead of this. e.g. nsName.String()
func NamespacedName(namespace, name string) string {
	return namespace + "/" + name
}

func StringExistsInSlice(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func RemoveStringFromSlice(slice []string, s string) (result []string) {
	for _, item := range slice {
		if item == s {
			continue
		}
		result = append(result, item)
	}
	return
}

func GetServiceEndpointSubset(svc *corev1.Service) corev1.EndpointSubset {
	var epSubset corev1.EndpointSubset
	for _, ip := range svc.Spec.ClusterIPs {
		epSubset.Addresses = append(epSubset.Addresses, corev1.EndpointAddress{IP: ip})
	}

	epSubset.Ports = GetServiceEndpointPorts(svc.Spec.Ports)
	return epSubset
}

// GetServiceEndpointPorts converts Service's port to EndpointPort
func GetServiceEndpointPorts(ports []corev1.ServicePort) []corev1.EndpointPort {
	if len(ports) == 0 {
		return nil
	}
	var epPorts []corev1.EndpointPort
	for _, p := range ports {
		epPorts = append(epPorts, corev1.EndpointPort{
			Name:     p.Name,
			Port:     p.Port,
			Protocol: p.Protocol,
		})
	}
	return epPorts
}
