// Copyright 2019 Antrea Authors
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

package openflow

import (
	"fmt"
	"math/rand"
	"net"

	"antrea.io/libOpenflow/openflow15"
	"antrea.io/libOpenflow/protocol"
	ofutil "antrea.io/libOpenflow/util"
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"antrea.io/antrea/pkg/agent/config"
	"antrea.io/antrea/pkg/agent/openflow/cookie"
	"antrea.io/antrea/pkg/agent/types"
	"antrea.io/antrea/pkg/agent/util"
	"antrea.io/antrea/pkg/apis/crd/v1alpha2"
	binding "antrea.io/antrea/pkg/ovs/openflow"
	utilip "antrea.io/antrea/pkg/util/ip"
	"antrea.io/antrea/third_party/proxy"
)

const maxRetryForOFSwitch = 5

// Client is the interface to program OVS flows for entity connectivity of Antrea.
type Client interface {
	// Initialize sets up all basic flows on the specific OVS bridge. It returns a channel which
	// is used to notify the caller in case of a reconnection, in which case ReplayFlows should
	// be called to ensure that the set of OVS flows is correct. All flows programmed in the
	// switch which match the current round number will be deleted before any new flow is
	// installed.
	Initialize(roundInfo types.RoundInfo,
		config *config.NodeConfig,
		networkConfig *config.NetworkConfig,
		egressConfig *config.EgressConfig,
		serviceConfig *config.ServiceConfig) (<-chan struct{}, error)

	// InstallNodeFlows should be invoked when a connection to a remote Node is going to be set
	// up. The hostname is used to identify the added flows. When IPsec tunnel is enabled,
	// ipsecTunOFPort must be set to the OFPort number of the IPsec tunnel port to the remote Node;
	// otherwise ipsecTunOFPort must be set to 0.
	// InstallNodeFlows has all-or-nothing semantics(call succeeds if all the flows are installed
	// successfully, otherwise no flows will be installed). Calls to InstallNodeFlows are idempotent.
	// Concurrent calls to InstallNodeFlows and / or UninstallNodeFlows are supported as long as they
	// are all for different hostnames.
	InstallNodeFlows(
		hostname string,
		peerConfigs map[*net.IPNet]net.IP,
		tunnelPeerIP *utilip.DualStackIPs,
		ipsecTunOFPort uint32,
		peerNodeMAC net.HardwareAddr) error

	// UninstallNodeFlows removes the connection to the remote Node specified with the
	// hostname. UninstallNodeFlows will do nothing if no connection to the host was established.
	UninstallNodeFlows(hostname string) error

	// InstallPodFlows should be invoked when a connection to a Pod on current Node. The
	// interfaceName is used to identify the added flows. InstallPodFlows has all-or-nothing
	// semantics(call succeeds if all the flows are installed successfully, otherwise no
	// flows will be installed). Calls to InstallPodFlows are idempotent. Concurrent calls
	// to InstallPodFlows and / or UninstallPodFlows are supported as long as they are all
	// for different interfaceNames.
	InstallPodFlows(interfaceName string, podInterfaceIPs []net.IP, podInterfaceMAC net.HardwareAddr, ofPort uint32, vlanID uint16) error

	// UninstallPodFlows removes the connection to the local Pod specified with the
	// interfaceName. UninstallPodFlows will do nothing if no connection to the Pod was established.
	UninstallPodFlows(interfaceName string) error

	// InstallServiceGroup installs a group for Service LB. Each endpoint
	// is a bucket of the group. For now, each bucket has the same weight.
	InstallServiceGroup(groupID binding.GroupIDType, withSessionAffinity bool, endpoints []proxy.Endpoint) error
	// UninstallGroup removes the group and its buckets that are
	// installed by InstallServiceGroup or InstallMulticastGroup.
	UninstallGroup(groupID binding.GroupIDType) error

	// InstallEndpointFlows installs flows for accessing Endpoints.
	// If an Endpoint is on the current Node, then flows for hairpin and endpoint
	// L2 forwarding should also be installed.
	InstallEndpointFlows(protocol binding.Protocol, endpoints []proxy.Endpoint) error
	// UninstallEndpointFlows removes flows of the Endpoint installed by
	// InstallEndpointFlows.
	UninstallEndpointFlows(protocol binding.Protocol, endpoint proxy.Endpoint) error

	// InstallServiceFlows installs flows for accessing Service NodePort, LoadBalancer and ClusterIP. It installs the
	// flow that uses the group/bucket to do service LB. If the affinityTimeout is not zero, it also installs the flow
	// which has a learn action to maintain the LB decision. The group with the groupID must be installed before,
	// otherwise the installation will fail.
	// nodeLocalExternal represents if the externalTrafficPolicy is Local or not. This field is meaningful only when
	// the svcType is NodePort or LoadBalancer.
	InstallServiceFlows(groupID binding.GroupIDType, svcIP net.IP, svcPort uint16, protocol binding.Protocol, affinityTimeout uint16, nodeLocalExternal bool, svcType v1.ServiceType) error
	// UninstallServiceFlows removes flows installed by InstallServiceFlows.
	UninstallServiceFlows(svcIP net.IP, svcPort uint16, protocol binding.Protocol) error

	// GetFlowTableStatus should return an array of flow table status, all existing flow tables should be included in the list.
	GetFlowTableStatus() []binding.TableStatus

	// InstallPolicyRuleFlows installs flows for a new NetworkPolicy rule. Rule should include all fields in the
	// NetworkPolicy rule. Each ingress/egress policy rule installs Openflow entries on two tables, one for
	// ruleTable and the other for dropTable. If a packet does not pass the ruleTable, it will be dropped by the
	// dropTable.
	InstallPolicyRuleFlows(ofPolicyRule *types.PolicyRule) error

	// BatchInstallPolicyRuleFlows installs multiple flows for NetworkPolicy rules in batch.
	BatchInstallPolicyRuleFlows(ofPolicyRules []*types.PolicyRule) error

	// UninstallPolicyRuleFlows removes the Openflow entry relevant to the specified NetworkPolicy rule.
	// It also returns a slice of stale ofPriorities used by ClusterNetworkPolicies.
	// UninstallPolicyRuleFlows will do nothing if no Openflow entry for the rule is installed.
	UninstallPolicyRuleFlows(ruleID uint32) ([]string, error)

	// AddPolicyRuleAddress adds one or multiple addresses to the specified NetworkPolicy rule. If addrType is true, the
	// addresses are added to PolicyRule.From, else to PolicyRule.To.
	AddPolicyRuleAddress(ruleID uint32, addrType types.AddressType, addresses []types.Address, priority *uint16, enableLogging bool) error

	// DeletePolicyRuleAddress removes addresses from the specified NetworkPolicy rule. If addrType is srcAddress, the addresses
	// are removed from PolicyRule.From, else from PolicyRule.To.
	DeletePolicyRuleAddress(ruleID uint32, addrType types.AddressType, addresses []types.Address, priority *uint16) error

	// InstallSNATMarkFlows installs flows for a local SNAT IP. On Linux, a
	// single flow is added to mark the packets tunnelled from remote Nodes
	// that should be SNAT'd with the SNAT IP.
	InstallSNATMarkFlows(snatIP net.IP, mark uint32) error

	// UninstallSNATMarkFlows removes the flows installed to set the packet
	// mark for a SNAT IP.
	UninstallSNATMarkFlows(mark uint32) error

	// InstallPodSNATFlows installs the SNAT flows for a local Pod. If the
	// SNAT IP for the Pod is on the local Node, a non-zero SNAT ID should
	// allocated for the SNAT IP, and the installed flow sets the SNAT IP
	// mark on the egress packets from the ofPort; if the SNAT IP is on a
	// remote Node, snatMark should be set to 0, and the installed flow
	// tunnels egress packets to the remote Node using the SNAT IP as the
	// tunnel destination, and the packets should be SNAT'd on the remote
	// Node. As of now, a Pod can be configured to use only a single SNAT
	// IP in a single address family (IPv4 or IPv6).
	InstallPodSNATFlows(ofPort uint32, snatIP net.IP, snatMark uint32) error

	// UninstallPodSNATFlows removes the SNAT flows for the local Pod.
	UninstallPodSNATFlows(ofPort uint32) error

	// Disconnect disconnects the connection between client and OFSwitch.
	Disconnect() error

	// IsConnected returns the connection status between client and OFSwitch. The return value is true if the OFSwitch is connected.
	IsConnected() bool

	// ReplayFlows should be called when a spurious disconnection occurs. After we reconnect to
	// the OFSwitch, we need to replay all the flows cached by the client. ReplayFlows will try
	// to replay as many flows as possible, and will log an error when a flow cannot be
	// installed.
	ReplayFlows()

	// DeleteStaleFlows deletes all flows from the previous round which are no longer needed. It
	// should be called by the agent after all required flows have been installed / updated with
	// the new round number.
	DeleteStaleFlows() error

	// GetTunnelVirtualMAC() returns GlobalVirtualMAC used for tunnel traffic.
	GetTunnelVirtualMAC() net.HardwareAddr

	// GetPodFlowKeys returns the keys (match strings) of the cached flows for a
	// Pod.
	GetPodFlowKeys(interfaceName string) []string

	// GetServiceFlowKeys returns the keys (match strings) of the cached
	// flows for a Service (port) and its endpoints.
	GetServiceFlowKeys(svcIP net.IP, svcPort uint16, protocol binding.Protocol, endpoints []proxy.Endpoint) []string

	// GetNetworkPolicyFlowKeys returns the keys (match strings) of the cached
	// flows for a NetworkPolicy. Flows are grouped by policy rules, and duplicated
	// entries can be added due to conjunctive match flows shared by multiple
	// rules.
	GetNetworkPolicyFlowKeys(npName, npNamespace string) []string

	// ReassignFlowPriorities takes a list of priority updates, and update the actionFlows to replace
	// the old priority with the desired one, for each priority update on that table.
	ReassignFlowPriorities(updates map[uint16]uint16, table uint8) error

	// SubscribePacketIn subscribes to packet in messages for the given reason. Packets
	// will be placed in the queue and if the queue is full, the packet in messages
	// will be dropped. pktInQueue supports rate-limiting for the consumer, in order to
	// constrain the compute resources that may be used by the consumer.
	SubscribePacketIn(reason uint8, pktInQueue *binding.PacketInQueue) error

	// SendTraceflowPacket injects packet to specified OVS port for Openflow.
	SendTraceflowPacket(dataplaneTag uint8, packet *binding.Packet, inPort uint32, outPort int32) error

	// InstallTraceflowFlows installs flows for a Traceflow request.
	InstallTraceflowFlows(dataplaneTag uint8, liveTraffic, droppedOnly, receiverOnly bool, packet *binding.Packet, ofPort uint32, timeoutSeconds uint16) error

	// UninstallTraceflowFlows uninstalls flows for a Traceflow request.
	UninstallTraceflowFlows(dataplaneTag uint8) error

	// Initial tun_metadata0 in TLV map for Traceflow.
	InitialTLVMap() error

	// Find Network Policy reference and OFpriority by conjunction ID.
	GetPolicyInfoFromConjunction(ruleID uint32) (string, string)

	// RegisterPacketInHandler uses SubscribePacketIn to get PacketIn message and process received
	// packets through registered handlers.
	RegisterPacketInHandler(packetHandlerReason uint8, packetHandlerName string, packetInHandler interface{})

	StartPacketInHandler(stopCh <-chan struct{})
	// Get traffic metrics of each NetworkPolicy rule.
	NetworkPolicyMetrics() map[uint32]*types.RuleMetric

	// Get multicast ingress metrics of each Pod in MulticastIngressPodMetricTable.
	MulticastIngressPodMetrics() map[uint32]*types.RuleMetric
	// Get multicast Pod ingress statistics from MulticastIngressPodMetricTable with specified ofPort.
	MulticastIngressPodMetricsByOFPort(ofPort int32) *types.RuleMetric
	// Get multicast egress metrics of each Pod in MulticastEgressPodMetricTable.
	MulticastEgressPodMetrics() map[string]*types.RuleMetric
	// Get multicast Pod ingress statistics from MulticastEgressPodMetricTable with specified src IP.
	MulticastEgressPodMetricsByIP(ip net.IP) *types.RuleMetric

	// SendTCPPacketOut sends TCP packet as a packet-out to OVS.
	SendTCPPacketOut(
		srcMAC string,
		dstMAC string,
		srcIP string,
		dstIP string,
		inPort uint32,
		outPort uint32,
		isIPv6 bool,
		tcpSrcPort uint16,
		tcpDstPort uint16,
		tcpAckNum uint32,
		tcpFlag uint8,
		mutatePacketOut func(builder binding.PacketOutBuilder) binding.PacketOutBuilder) error
	// SendICMPPacketOut sends ICMP packet as a packet-out to OVS.
	SendICMPPacketOut(
		srcMAC string,
		dstMAC string,
		srcIP string,
		dstIP string,
		inPort uint32,
		outPort uint32,
		isIPv6 bool,
		icmpType uint8,
		icmpCode uint8,
		icmpData []byte,
		mutatePacketOut func(builder binding.PacketOutBuilder) binding.PacketOutBuilder) error
	// SendUDPPacketOut sends UDP packet as a packet-out to OVS.
	SendUDPPacketOut(
		srcMAC string,
		dstMAC string,
		srcIP string,
		dstIP string,
		inPort uint32,
		outPort uint32,
		isIPv6 bool,
		udpSrcPort uint16,
		udpDstPort uint16,
		udpData []byte,
		mutatePacketOut func(builder binding.PacketOutBuilder) binding.PacketOutBuilder) error
	// NewDNSpacketInConjunction creates a policyRuleConjunction for the dns response interception flows.
	NewDNSpacketInConjunction(id uint32) error
	// AddAddressToDNSConjunction adds addresses to the toAddresses of the dns packetIn conjunction,
	// so that dns response packets sent towards these addresses will be intercepted and parsed by
	// the fqdnController.
	AddAddressToDNSConjunction(id uint32, addrs []types.Address) error
	// DeleteAddressFromDNSConjunction removes addresses from the toAddresses of the dns packetIn conjunction.
	DeleteAddressFromDNSConjunction(id uint32, addrs []types.Address) error
	// InstallMulticastInitialFlows installs OpenFlow to packetIn the IGMP messages and output the Multicast traffic to
	// antrea-gw0 so that local Pods could access external Multicast servers.
	InstallMulticastInitialFlows(pktInReason uint8) error

	// InstallMulticastFlows installs the flow to forward Multicast traffic normally, and output it to antrea-gw0
	// to ensure it can be forwarded to the external addresses.
	InstallMulticastFlows(multicastIP net.IP, groupID binding.GroupIDType) error

	// UninstallMulticastFlows removes the flow matching the given multicastIP.
	UninstallMulticastFlows(multicastIP net.IP) error

	// InstallMulticastRemoteReportFlows installs flows to forward the IGMP report messages to the other Nodes,
	// and packetIn the report messages to Antrea Agent which is received via tunnel port.
	// The OpenFlow group identified by groupID is used to forward packet to all other Nodes in the cluster
	// over tunnel.
	InstallMulticastRemoteReportFlows(groupID binding.GroupIDType) error
	// SendIGMPQueryPacketOut sends the IGMPQuery packet as a packet-out to OVS from the gateway port.
	SendIGMPQueryPacketOut(
		dstMAC net.HardwareAddr,
		dstIP net.IP,
		outPort uint32,
		igmp ofutil.Message) error

	// InstallTrafficControlMarkFlows installs the flows to mark the packets for a traffic control rule.
	InstallTrafficControlMarkFlows(name string, sourceOFPorts []uint32, targetOFPort uint32, direction v1alpha2.Direction, action v1alpha2.TrafficControlAction) error

	// UninstallTrafficControlMarkFlows removes the flows for a traffic control rule.
	UninstallTrafficControlMarkFlows(name string) error

	// InstallTrafficControlReturnPortFlow installs the flow to classify the packets from a return port.
	InstallTrafficControlReturnPortFlow(returnOFPort uint32) error

	// UninstallTrafficControlReturnPortFlow removes the flow to classify the packets from a return port.
	UninstallTrafficControlReturnPortFlow(returnOFPort uint32) error

	InstallMulticastGroup(ofGroupID binding.GroupIDType, localReceivers []uint32, remoteNodeReceivers []net.IP) error

	// SendIGMPRemoteReportPacketOut sends the IGMP report packet as a packet-out to remote Nodes via the tunnel port.
	SendIGMPRemoteReportPacketOut(
		dstMAC net.HardwareAddr,
		dstIP net.IP,
		igmp ofutil.Message) error

	// InstallMulticlusterNodeFlows installs flows to handle cross-cluster packets between a regular
	// Node and a local Gateway.
	InstallMulticlusterNodeFlows(
		clusterID string,
		peerConfigs map[*net.IPNet]net.IP,
		tunnelPeerIP net.IP) error

	// InstallMulticlusterGatewayFlows installs flows to handle cross-cluster packets between Gateways.
	InstallMulticlusterGatewayFlows(
		clusterID string,
		peerConfigs map[*net.IPNet]net.IP,
		tunnelPeerIP net.IP,
		localGatewayIP net.IP) error

	// InstallMulticlusterClassifierFlows installs flows to classify cross-cluster packets.
	InstallMulticlusterClassifierFlows(tunnelOFPort uint32, isGateway bool) error

	// UninstallMulticlusterFlows removes cross-cluster flows matching the given clusterID on
	// a regular Node or a Gateway.
	UninstallMulticlusterFlows(clusterID string) error

	// InstallVMUplinkFlows installs flows to forward packet between uplinkPort and hostPort. On a VM, the
	// uplink and host internal port are paired directly, and no layer 2/3 forwarding flow is installed.
	InstallVMUplinkFlows(hostInterfaceName string, hostPort int32, uplinkPort int32) error

	// UninstallVMUplinkFlows removes the flows installed to forward packet between uplinkPort and hostPort.
	UninstallVMUplinkFlows(hostInterfaceName string) error

	// InstallPolicyBypassFlows installs flows to bypass the NetworkPolicy rules on the traffic with the given ipnet
	// or ip, port, protocol and direction. It is used to bypass NetworkPolicy enforcement on a VM for the particular
	// traffic.
	InstallPolicyBypassFlows(protocol binding.Protocol, ipNet *net.IPNet, port uint16, isIngress bool) error
}

// GetFlowTableStatus returns an array of flow table status.
func (c *client) GetFlowTableStatus() []binding.TableStatus {
	return c.bridge.DumpTableStatus()
}

// IsConnected returns the connection status between client and OFSwitch.
func (c *client) IsConnected() bool {
	return c.bridge.IsConnected()
}

// addFlows installs the flows on the OVS bridge and then add them into the flow cache. If the flow cache exists,
// it will return immediately, otherwise it will use Bundle to add all flows, and then add them into the flow cache.
// If it fails to add the flows with Bundle, it will return the error and no flow cache is created.
func (c *client) addFlows(cache *flowCategoryCache, flowCacheKey string, flows []binding.Flow) error {
	return c.addFlowsWithMultipleKeys(cache, map[string][]binding.Flow{flowCacheKey: flows})
}

// addFlowsWithMultipleKeys installs the flows with different flowCache keys and adds them into the cache on success.
// It will skip flows whose cache already exists. All flows will be installed via a bundle.
func (c *client) addFlowsWithMultipleKeys(cache *flowCategoryCache, keyToFlows map[string][]binding.Flow) error {
	// allFlows keeps the flows we will install via a bundle.
	var allFlows []binding.Flow
	// flowCacheMap keeps the flowCache items we will add to the cache on bundle success.
	flowCacheMap := map[string]flowCache{}
	for flowCacheKey, flows := range keyToFlows {
		_, ok := cache.Load(flowCacheKey)
		// If a flow cache entry already exists for the key, skip it.
		if ok {
			klog.V(2).InfoS("Flows with this cache key are already installed", "key", flowCacheKey)
			continue
		}
		fCache := flowCache{}
		for _, flow := range flows {
			allFlows = append(allFlows, flow)
			fCache[flow.MatchString()] = flow
		}
		flowCacheMap[flowCacheKey] = fCache
	}
	if len(allFlows) == 0 {
		return nil
	}
	err := c.ofEntryOperations.AddAll(allFlows)
	if err != nil {
		return err
	}
	// Add the installed flows into the flow cache.
	for flowCacheKey, flowCache := range flowCacheMap {
		cache.Store(flowCacheKey, flowCache)
	}
	return nil
}

// modifyFlows sets the flows of flowCategoryCache be exactly same as the provided slice for the given flowCacheKey.
func (c *client) modifyFlows(cache *flowCategoryCache, flowCacheKey string, flows []binding.Flow) error {
	oldFlowCacheI, ok := cache.Load(flowCacheKey)
	fCache := flowCache{}
	var err error
	if !ok {
		for _, flow := range flows {
			fCache[flow.MatchString()] = flow
		}

		err = c.ofEntryOperations.AddAll(flows)
	} else {
		var adds, mods, dels []binding.Flow
		oldFlowCache := oldFlowCacheI.(flowCache)
		for _, flow := range flows {
			matchString := flow.MatchString()
			if _, ok := oldFlowCache[matchString]; ok {
				mods = append(mods, flow)
			} else {
				adds = append(adds, flow)
			}
			fCache[matchString] = flow
		}
		for k, v := range oldFlowCache {
			if _, ok := fCache[k]; !ok {
				dels = append(dels, v)
			}
		}
		err = c.ofEntryOperations.BundleOps(adds, mods, dels)
	}
	if err != nil {
		return err
	}

	// Modify the flows in the flow cache.
	cache.Store(flowCacheKey, fCache)
	return nil
}

// deleteFlows deletes all the flows in the flow cache indexed by the provided flowCacheKey.
func (c *client) deleteFlows(cache *flowCategoryCache, flowCacheKey string) error {
	fCacheI, ok := cache.Load(flowCacheKey)
	if !ok {
		// no matching flows found in the cache
		return nil
	}
	fCache := fCacheI.(flowCache)
	// Delete flows from OVS.
	delFlows := make([]binding.Flow, 0, len(fCache))
	for _, flow := range fCache {
		delFlows = append(delFlows, flow)
	}
	if err := c.ofEntryOperations.DeleteAll(delFlows); err != nil {
		return err
	}
	cache.Delete(flowCacheKey)
	return nil
}

// InstallNodeFlows installs flows for peer Nodes. Parameter remoteGatewayMAC is only for Windows.
func (c *client) InstallNodeFlows(hostname string,
	peerConfigs map[*net.IPNet]net.IP,
	tunnelPeerIPs *utilip.DualStackIPs,
	ipsecTunOFPort uint32,
	remoteGatewayMAC net.HardwareAddr,
) error {
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()

	var flows []binding.Flow
	localGatewayMAC := c.nodeConfig.GatewayConfig.MAC
	for peerPodCIDR, peerGatewayIP := range peerConfigs {
		isIPv6 := peerGatewayIP.To4() == nil
		tunnelPeerIP := tunnelPeerIPs.IPv4
		if isIPv6 {
			tunnelPeerIP = tunnelPeerIPs.IPv6
		} else {
			// Since broadcast is not supported in IPv6, ARP should happen only with IPv4 address, and ARP responder flows
			// only work for IPv4 addresses.
			// arpResponderFlow() adds a flow to resolve peer gateway IPs to GlobalVirtualMAC.
			// This flow replies to ARP requests sent from the local gateway asking for the MAC address of a remote peer gateway. It ensures that the local Node can reach any remote Pod.
			flows = append(flows, c.featurePodConnectivity.arpResponderFlow(peerGatewayIP, GlobalVirtualMAC))
		}
		// tunnelPeerIP is the Node Internal Address. In a dual-stack setup, one Node has 2 Node Internal
		// Addresses (IPv4 and IPv6) .
		if (!isIPv6 && c.networkConfig.NeedsTunnelToPeer(tunnelPeerIPs.IPv4, c.nodeConfig.NodeTransportIPv4Addr)) ||
			(isIPv6 && c.networkConfig.NeedsTunnelToPeer(tunnelPeerIPs.IPv6, c.nodeConfig.NodeTransportIPv6Addr)) {
			flows = append(flows, c.featurePodConnectivity.l3FwdFlowToRemoteViaTun(localGatewayMAC, *peerPodCIDR, tunnelPeerIP))
		} else {
			flows = append(flows, c.featurePodConnectivity.l3FwdFlowToRemoteViaRouting(localGatewayMAC, remoteGatewayMAC, tunnelPeerIP, peerPodCIDR)...)
		}
		if c.enableEgress {
			flows = append(flows, c.featureEgress.snatSkipNodeFlow(tunnelPeerIP))
		}
		if c.connectUplinkToBridge {
			// flow to catch traffic from AntreaFlexibleIPAM Pod to remote Per-Node IPAM Pod
			flows = append(flows, c.featurePodConnectivity.l3FwdFlowToRemoteViaUplink(remoteGatewayMAC, *peerPodCIDR, true))
		}
	}
	if ipsecTunOFPort != 0 {
		// When IPsec tunnel is enabled, packets received from the remote Node are
		// input from the Node's IPsec tunnel port, not the default tunnel port. So,
		// add a separate tunnelClassifierFlow for the IPsec tunnel port.
		flows = append(flows, c.featurePodConnectivity.tunnelClassifierFlow(ipsecTunOFPort))
	}

	// For Windows Noencap Mode, the OVS flows for Node need to be exactly same as the provided 'flows' slice because
	// the Node flows may be processed more than once if the MAC annotation is updated.
	return c.modifyFlows(c.featurePodConnectivity.nodeCachedFlows, hostname, flows)
}

func (c *client) UninstallNodeFlows(hostname string) error {
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()
	return c.deleteFlows(c.featurePodConnectivity.nodeCachedFlows, hostname)
}

func (c *client) InstallPodFlows(interfaceName string, podInterfaceIPs []net.IP, podInterfaceMAC net.HardwareAddr, ofPort uint32, vlanID uint16) error {
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()

	podInterfaceIPv4 := util.GetIPv4Addr(podInterfaceIPs)
	// TODO(gran): support IPv6
	isAntreaFlexibleIPAM := c.connectUplinkToBridge && c.nodeConfig.PodIPv4CIDR != nil && !c.nodeConfig.PodIPv4CIDR.Contains(podInterfaceIPv4)

	localGatewayMAC := c.nodeConfig.GatewayConfig.MAC
	flows := []binding.Flow{
		c.featurePodConnectivity.podClassifierFlow(ofPort, isAntreaFlexibleIPAM),
		c.featurePodConnectivity.l2ForwardCalcFlow(podInterfaceMAC, ofPort),
	}

	// Add support for IPv4 ARP responder.
	if podInterfaceIPv4 != nil {
		flows = append(flows, c.featurePodConnectivity.arpSpoofGuardFlow(podInterfaceIPv4, podInterfaceMAC, ofPort))
	}
	// Add IP SpoofGuard flows for all validate IPs.
	flows = append(flows, c.featurePodConnectivity.podIPSpoofGuardFlow(podInterfaceIPs, podInterfaceMAC, ofPort, vlanID)...)
	// Add L3 Routing flows to rewrite Pod's dst MAC for all validate IPs.
	flows = append(flows, c.featurePodConnectivity.l3FwdFlowToPod(localGatewayMAC, podInterfaceIPs, podInterfaceMAC, isAntreaFlexibleIPAM, vlanID)...)

	if c.networkConfig.TrafficEncapMode.IsNetworkPolicyOnly() {
		// In policy-only mode, traffic to local Pod is routed based on destination IP.
		flows = append(flows,
			c.featurePodConnectivity.l3FwdFlowRouteToPod(podInterfaceIPs, podInterfaceMAC)...,
		)
	}

	if isAntreaFlexibleIPAM {
		// Add Pod uplink classifier flows for AntreaFlexibleIPAM Pods.
		flows = append(flows, c.featurePodConnectivity.podUplinkClassifierFlows(podInterfaceMAC, vlanID)...)
		if vlanID > 0 {
			flows = append(flows, c.featurePodConnectivity.podVLANFlow(ofPort, vlanID))
		}
	}
	err := c.addFlows(c.featurePodConnectivity.podCachedFlows, interfaceName, flows)
	if err != nil {
		return err
	}
	// Multicast pod statistics is currently only supported for pods running IPv4 address.
	if c.enableMulticast && podInterfaceIPv4 != nil {
		return c.installMulticastPodMetricFlows(interfaceName, podInterfaceIPv4, ofPort)
	}
	return nil
}

func (c *client) installMulticastPodMetricFlows(interfaceName string, podIP net.IP, ofPort uint32) error {
	flows := c.featureMulticast.multicastPodMetricFlows(podIP, ofPort)
	cacheKey := fmt.Sprintf("multicast_pod_metric_%s", interfaceName)
	return c.addFlows(c.featureMulticast.cachedFlows, cacheKey, flows)
}

func (c *client) UninstallPodFlows(interfaceName string) error {
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()
	err := c.deleteFlows(c.featurePodConnectivity.podCachedFlows, interfaceName)
	if err != nil {
		return err
	}
	if c.enableMulticast {
		cacheKey := fmt.Sprintf("multicast_pod_metric_%s", interfaceName)
		return c.deleteFlows(c.featureMulticast.cachedFlows, cacheKey)
	}
	return nil
}

func (c *client) getFlowKeysFromCache(cache *flowCategoryCache, cacheKey string) []string {
	fCacheI, ok := cache.Load(cacheKey)
	if !ok {
		return nil
	}
	fCache := fCacheI.(flowCache)
	flowKeys := make([]string, 0, len(fCache))

	// ReplayFlows() could change Flow internal state. Although its current
	// implementation does not impact Flow match string generation, we still
	// acquire read lock of replayMutex here for logic cleanliness.
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()
	for _, flow := range fCache {
		flowKeys = append(flowKeys, flow.MatchString())
	}
	return flowKeys
}

func (c *client) GetPodFlowKeys(interfaceName string) []string {
	return c.getFlowKeysFromCache(c.featurePodConnectivity.podCachedFlows, interfaceName)
}

func (c *client) InstallServiceGroup(groupID binding.GroupIDType, withSessionAffinity bool, endpoints []proxy.Endpoint) error {
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()

	group := c.featureService.serviceEndpointGroup(groupID, withSessionAffinity, endpoints...)
	if err := group.Add(); err != nil {
		return fmt.Errorf("error when installing Service Endpoints Group: %w", err)
	}
	c.featureService.groupCache.Store(groupID, group)
	return nil
}

func (c *client) UninstallGroup(groupID binding.GroupIDType) error {
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()
	if !c.bridge.DeleteGroup(groupID) {
		return fmt.Errorf("group %d delete failed", groupID)
	}
	c.featureService.groupCache.Delete(groupID)
	return nil
}

func generateEndpointFlowCacheKey(endpointIP string, endpointPort int, protocol binding.Protocol) string {
	return fmt.Sprintf("E%s%s%x", endpointIP, protocol, endpointPort)
}

func generateServicePortFlowCacheKey(svcIP net.IP, svcPort uint16, protocol binding.Protocol) string {
	return fmt.Sprintf("S%s%s%x", svcIP, protocol, svcPort)
}

func (c *client) InstallEndpointFlows(protocol binding.Protocol, endpoints []proxy.Endpoint) error {
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()

	// keyToFlows is a map from the flows' cache key to the flows.
	keyToFlows := map[string][]binding.Flow{}
	for _, endpoint := range endpoints {
		var flows []binding.Flow
		endpointPort, _ := endpoint.Port()
		endpointIP := net.ParseIP(endpoint.IP())
		portVal := util.PortToUint16(endpointPort)
		cacheKey := generateEndpointFlowCacheKey(endpoint.IP(), endpointPort, protocol)
		flows = append(flows, c.featureService.endpointDNATFlow(endpointIP, portVal, protocol))
		if endpoint.GetIsLocal() {
			flows = append(flows, c.featureService.podHairpinSNATFlow(endpointIP))
		}
		keyToFlows[cacheKey] = flows
	}

	return c.addFlowsWithMultipleKeys(c.featureService.cachedFlows, keyToFlows)
}

func (c *client) UninstallEndpointFlows(protocol binding.Protocol, endpoint proxy.Endpoint) error {
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()

	port, err := endpoint.Port()
	if err != nil {
		return fmt.Errorf("error when getting port: %w", err)
	}
	cacheKey := generateEndpointFlowCacheKey(endpoint.IP(), port, protocol)
	return c.deleteFlows(c.featureService.cachedFlows, cacheKey)
}

func (c *client) InstallServiceFlows(groupID binding.GroupIDType, svcIP net.IP, svcPort uint16, protocol binding.Protocol, affinityTimeout uint16, nodeLocalExternal bool, svcType v1.ServiceType) error {
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()
	var flows []binding.Flow
	flows = append(flows, c.featureService.serviceLBFlow(groupID, svcIP, svcPort, protocol, affinityTimeout != 0, nodeLocalExternal, svcType))
	if affinityTimeout != 0 {
		flows = append(flows, c.featureService.serviceLearnFlow(groupID, svcIP, svcPort, protocol, affinityTimeout, nodeLocalExternal, svcType))
	}
	cacheKey := generateServicePortFlowCacheKey(svcIP, svcPort, protocol)
	return c.addFlows(c.featureService.cachedFlows, cacheKey, flows)
}

func (c *client) UninstallServiceFlows(svcIP net.IP, svcPort uint16, protocol binding.Protocol) error {
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()
	cacheKey := generateServicePortFlowCacheKey(svcIP, svcPort, protocol)
	return c.deleteFlows(c.featureService.cachedFlows, cacheKey)
}

func (c *client) GetServiceFlowKeys(svcIP net.IP, svcPort uint16, protocol binding.Protocol, endpoints []proxy.Endpoint) []string {
	cacheKey := generateServicePortFlowCacheKey(svcIP, svcPort, protocol)
	flowKeys := c.getFlowKeysFromCache(c.featureService.cachedFlows, cacheKey)
	for _, ep := range endpoints {
		epPort, _ := ep.Port()
		cacheKey = generateEndpointFlowCacheKey(ep.IP(), epPort, protocol)
		flowKeys = append(flowKeys, c.getFlowKeysFromCache(c.featureService.cachedFlows, cacheKey)...)
	}
	return flowKeys
}

func (c *client) initialize() error {
	if err := c.ofEntryOperations.AddAll(c.defaultFlows()); err != nil {
		return fmt.Errorf("failed to install default flows: %v", err)
	}

	for _, activeFeature := range c.activatedFeatures {
		if err := c.ofEntryOperations.AddAll(activeFeature.initFlows()); err != nil {
			return fmt.Errorf("failed to install feature %v initial flows: %v", activeFeature.getFeatureName(), err)
		}
	}

	if c.ovsMetersAreSupported {
		if err := c.genPacketInMeter(PacketInMeterIDNP, PacketInMeterRateNP).Add(); err != nil {
			return fmt.Errorf("failed to install OpenFlow meter entry (meterID:%d, rate:%d) for NetworkPolicy packet-in rate limiting: %v", PacketInMeterIDNP, PacketInMeterRateNP, err)
		}
		if err := c.genPacketInMeter(PacketInMeterIDTF, PacketInMeterRateTF).Add(); err != nil {
			return fmt.Errorf("failed to install OpenFlow meter entry (meterID:%d, rate:%d) for TraceFlow packet-in rate limiting: %v", PacketInMeterIDTF, PacketInMeterRateTF, err)
		}
	}
	return nil
}

func (c *client) Initialize(roundInfo types.RoundInfo,
	nodeConfig *config.NodeConfig,
	networkConfig *config.NetworkConfig,
	egressConfig *config.EgressConfig,
	serviceConfig *config.ServiceConfig) (<-chan struct{}, error) {
	c.nodeConfig = nodeConfig
	c.networkConfig = networkConfig
	c.egressConfig = egressConfig
	c.serviceConfig = serviceConfig
	c.nodeType = nodeConfig.Type

	if networkConfig.IPv4Enabled {
		c.ipProtocols = append(c.ipProtocols, binding.ProtocolIP)
	}
	if networkConfig.IPv6Enabled {
		c.ipProtocols = append(c.ipProtocols, binding.ProtocolIPv6)
	}
	c.roundInfo = roundInfo
	c.cookieAllocator = cookie.NewAllocator(roundInfo.RoundNum)
	c.generatePipelines()
	c.realizePipelines()

	// Initiate connections to target OFswitch, and create tables on the switch.
	connCh := make(chan struct{})
	if err := c.bridge.Connect(maxRetryForOFSwitch, connCh); err != nil {
		return nil, err
	}

	// Ignore first notification, it is not a "reconnection".
	<-connCh

	// In the normal case, there should be no existing flows with the current round number. This
	// is needed in case the agent was restarted before we had a chance to increment the round
	// number (incrementing the round number happens once we are satisfied that stale flows from
	// the previous round have been deleted).
	if err := c.deleteFlowsByRoundNum(roundInfo.RoundNum); err != nil {
		return nil, fmt.Errorf("error when deleting exiting flows for current round number: %v", err)
	}

	// In the normal case, there should be no existing meter entries. This is needed in case the
	// antrea-agent container is restarted (but not the antrea-ovs one), which will add meter
	// entries during initialization, but the meter entries added during the previous
	// initialization still exist. Trying to add an existing meter entry will cause an
	// OFPMMFC_METER_EXISTS error.
	if c.ovsMetersAreSupported {
		if err := c.bridge.DeleteMeterAll(); err != nil {
			return nil, fmt.Errorf("error when deleting all meter entries: %v", err)
		}
	}

	return connCh, c.initialize()
}

// generatePipelines generates table list for every pipeline from all activated features. Note that, tables are not realized
// in OVS bridge in this function.
func (c *client) generatePipelines() {
	if c.nodeType == config.K8sNode {
		c.featurePodConnectivity = newFeaturePodConnectivity(c.cookieAllocator,
			c.ipProtocols,
			c.nodeConfig,
			c.networkConfig,
			c.connectUplinkToBridge,
			c.enableMulticast,
			c.proxyAll,
			c.enableTrafficControl)
		c.activatedFeatures = append(c.activatedFeatures, c.featurePodConnectivity)
		c.traceableFeatures = append(c.traceableFeatures, c.featurePodConnectivity)

		c.featureService = newFeatureService(c.cookieAllocator,
			c.ipProtocols,
			c.nodeConfig,
			c.networkConfig,
			c.serviceConfig,
			c.bridge,
			c.enableProxy,
			c.proxyAll,
			c.connectUplinkToBridge)
		c.activatedFeatures = append(c.activatedFeatures, c.featureService)
		c.traceableFeatures = append(c.traceableFeatures, c.featureService)
	}

	if c.nodeType == config.ExternalNode {
		c.featureExternalNodeConnectivity = newFeatureExternalNodeConnectivity(c.cookieAllocator, c.ipProtocols)
		c.activatedFeatures = append(c.activatedFeatures, c.featureExternalNodeConnectivity)
	}

	c.featureNetworkPolicy = newFeatureNetworkPolicy(c.cookieAllocator,
		c.ipProtocols,
		c.bridge,
		c.ovsMetersAreSupported,
		c.enableDenyTracking,
		c.enableAntreaPolicy,
		c.enableMulticast,
		c.proxyAll,
		c.connectUplinkToBridge,
		c.nodeType)
	c.activatedFeatures = append(c.activatedFeatures, c.featureNetworkPolicy)
	c.traceableFeatures = append(c.traceableFeatures, c.featureNetworkPolicy)

	if c.enableEgress {
		c.featureEgress = newFeatureEgress(c.cookieAllocator, c.ipProtocols, c.nodeConfig, c.egressConfig)
		c.activatedFeatures = append(c.activatedFeatures, c.featureEgress)
	}

	if c.enableMulticast {
		// TODO: add support for IPv6 protocol
		c.featureMulticast = newFeatureMulticast(c.cookieAllocator, []binding.Protocol{binding.ProtocolIP}, c.bridge, c.enableAntreaPolicy, c.nodeConfig.GatewayConfig.OFPort, c.networkConfig.TrafficEncapMode.SupportsEncap(), config.DefaultTunOFPort)
		c.activatedFeatures = append(c.activatedFeatures, c.featureMulticast)
	}

	if c.enableMulticluster {
		c.featureMulticluster = newFeatureMulticluster(c.cookieAllocator, []binding.Protocol{binding.ProtocolIP})
		c.activatedFeatures = append(c.activatedFeatures, c.featureMulticluster)
	}

	c.featureTraceflow = newFeatureTraceflow()
	c.activatedFeatures = append(c.activatedFeatures, c.featureTraceflow)

	// Pipelines to generate.
	pipelineIDs := []binding.PipelineID{pipelineRoot, pipelineIP}
	if c.networkConfig.IPv4Enabled {
		pipelineIDs = append(pipelineIDs, pipelineARP)
		if c.enableMulticast {
			pipelineIDs = append(pipelineIDs, pipelineMulticast)
		}
	}
	if c.nodeType == config.ExternalNode {
		pipelineIDs = append(pipelineIDs, pipelineNonIP)
	}

	// For every pipeline, get required tables from every active feature and store the required tables in a map to avoid
	// duplication.
	pipelineRequiredTablesMap := make(map[binding.PipelineID]map[*Table]struct{})
	for _, pipelineID := range pipelineIDs {
		pipelineRequiredTablesMap[pipelineID] = make(map[*Table]struct{})
	}
	pipelineRequiredTablesMap[pipelineRoot][PipelineRootClassifierTable] = struct{}{}

	for _, f := range c.activatedFeatures {
		for _, t := range f.getRequiredTables() {
			if _, ok := pipelineRequiredTablesMap[t.pipeline]; ok {
				pipelineRequiredTablesMap[t.pipeline][t] = struct{}{}
			}
		}
	}

	for pipelineID := firstPipeline; pipelineID <= lastPipeline; pipelineID++ {
		if _, ok := pipelineRequiredTablesMap[pipelineID]; !ok {
			continue
		}
		var requiredTables []*Table
		// Iterate the table order cache to generate a sorted table list with required tables.
		for _, table := range tableOrderCache[pipelineID] {
			if _, ok := pipelineRequiredTablesMap[pipelineID][table]; ok {
				requiredTables = append(requiredTables, table)
			}
		}
		if len(requiredTables) == 0 {
			klog.InfoS("There is no required table for the pipeline ID, skip generating pipeline", "pipeline", pipelineID)
			continue
		}
		// generate a pipeline from the required table list.
		c.pipelines[pipelineID] = generatePipeline(pipelineID, requiredTables)
	}
}

func (c *client) InstallSNATMarkFlows(snatIP net.IP, mark uint32) error {
	flow := c.featureEgress.snatIPFromTunnelFlow(snatIP, mark)
	cacheKey := fmt.Sprintf("s%x", mark)
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()
	return c.addFlows(c.featureEgress.cachedFlows, cacheKey, []binding.Flow{flow})
}

func (c *client) UninstallSNATMarkFlows(mark uint32) error {
	cacheKey := fmt.Sprintf("s%x", mark)
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()
	return c.deleteFlows(c.featureEgress.cachedFlows, cacheKey)
}

func (c *client) InstallPodSNATFlows(ofPort uint32, snatIP net.IP, snatMark uint32) error {
	flows := []binding.Flow{c.featureEgress.snatRuleFlow(ofPort, snatIP, snatMark, c.nodeConfig.GatewayConfig.MAC)}
	cacheKey := fmt.Sprintf("p%x", ofPort)
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()
	return c.addFlows(c.featureEgress.cachedFlows, cacheKey, flows)
}

func (c *client) UninstallPodSNATFlows(ofPort uint32) error {
	cacheKey := fmt.Sprintf("p%x", ofPort)
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()
	return c.deleteFlows(c.featureEgress.cachedFlows, cacheKey)
}

func (c *client) ReplayFlows() {
	c.replayMutex.Lock()
	defer c.replayMutex.Unlock()

	if err := c.initialize(); err != nil {
		klog.Errorf("Error during flow replay: %v", err)
	}

	if c.featureService != nil {
		c.featureService.replayGroups()
	}
	if c.enableMulticast {
		c.featureMulticast.replayGroups()
	}

	for _, activeFeature := range c.activatedFeatures {
		if err := c.ofEntryOperations.AddAll(activeFeature.replayFlows()); err != nil {
			klog.ErrorS(err, "Error when replaying feature flows", "feature", activeFeature.getFeatureName())
		}
	}
}

func (c *client) deleteFlowsByRoundNum(roundNum uint64) error {
	cookieID, cookieMask := cookie.CookieMaskForRound(roundNum)
	return c.bridge.DeleteFlowsByCookie(cookieID, cookieMask)
}

func (c *client) DeleteStaleFlows() error {
	if c.roundInfo.PrevRoundNum == nil {
		klog.V(2).Info("Previous round number is unset, no flows to delete")
		return nil
	}
	return c.deleteFlowsByRoundNum(*c.roundInfo.PrevRoundNum)
}

func (c *client) SubscribePacketIn(reason uint8, pktInQueue *binding.PacketInQueue) error {
	return c.bridge.SubscribePacketIn(reason, pktInQueue)
}

func (c *client) SendTraceflowPacket(dataplaneTag uint8, packet *binding.Packet, inPort uint32, outPort int32) error {
	packetOutBuilder := c.bridge.BuildPacketOut()

	if packet.DestinationMAC == nil {
		packet.DestinationMAC = c.nodeConfig.GatewayConfig.MAC
	}
	// Set ethernet header
	packetOutBuilder = packetOutBuilder.SetDstMAC(packet.DestinationMAC).SetSrcMAC(packet.SourceMAC)

	// Set IP header
	packetOutBuilder = packetOutBuilder.SetDstIP(packet.DestinationIP).SetSrcIP(packet.SourceIP).SetTTL(packet.TTL)
	if !packet.IsIPv6 {
		packetOutBuilder = packetOutBuilder.SetIPFlags(packet.IPFlags)
	}

	// Set transport header
	switch packet.IPProto {
	case protocol.Type_ICMP, protocol.Type_IPv6ICMP:
		if packet.IPProto == protocol.Type_ICMP {
			packetOutBuilder = packetOutBuilder.SetIPProtocol(binding.ProtocolICMP)
		} else {
			packetOutBuilder = packetOutBuilder.SetIPProtocol(binding.ProtocolICMPv6)
		}
		packetOutBuilder = packetOutBuilder.SetICMPType(packet.ICMPType).
			SetICMPCode(packet.ICMPCode).
			SetICMPID(packet.ICMPEchoID).
			SetICMPSequence(packet.ICMPEchoSeq)
	case protocol.Type_TCP:
		if packet.IsIPv6 {
			packetOutBuilder = packetOutBuilder.SetIPProtocol(binding.ProtocolTCPv6)
		} else {
			packetOutBuilder = packetOutBuilder.SetIPProtocol(binding.ProtocolTCP)
		}
		tcpSrcPort := packet.SourcePort
		if tcpSrcPort == 0 {
			// #nosec G404: random number generator not used for security purposes.
			tcpSrcPort = uint16(rand.Uint32())
		}
		packetOutBuilder = packetOutBuilder.SetTCPDstPort(packet.DestinationPort).
			SetTCPSrcPort(tcpSrcPort).
			SetTCPFlags(packet.TCPFlags)
	case protocol.Type_UDP:
		if packet.IsIPv6 {
			packetOutBuilder = packetOutBuilder.SetIPProtocol(binding.ProtocolUDPv6)
		} else {
			packetOutBuilder = packetOutBuilder.SetIPProtocol(binding.ProtocolUDP)
		}
		packetOutBuilder = packetOutBuilder.SetUDPDstPort(packet.DestinationPort).
			SetUDPSrcPort(packet.SourcePort)
	default:
		packetOutBuilder = packetOutBuilder.SetIPProtocolValue(packet.IsIPv6, packet.IPProto)
	}

	packetOutBuilder = packetOutBuilder.SetInport(inPort)
	if outPort != -1 {
		packetOutBuilder = packetOutBuilder.SetOutport(uint32(outPort))
	}
	packetOutBuilder = packetOutBuilder.AddSetIPTOSAction(dataplaneTag)
	packetOutObj := packetOutBuilder.Done()
	return c.bridge.SendPacketOut(packetOutObj)
}

func (c *client) InstallTraceflowFlows(dataplaneTag uint8, liveTraffic, droppedOnly, receiverOnly bool, packet *binding.Packet, ofPort uint32, timeoutSeconds uint16) error {
	cacheKey := fmt.Sprintf("%x", dataplaneTag)
	var flows []binding.Flow
	for _, f := range c.traceableFeatures {
		flows = append(flows, f.flowsToTrace(dataplaneTag,
			c.ovsMetersAreSupported,
			liveTraffic,
			droppedOnly,
			receiverOnly,
			packet,
			ofPort,
			timeoutSeconds)...)
	}
	return c.addFlows(c.featureTraceflow.cachedFlows, cacheKey, flows)
}

func (c *client) UninstallTraceflowFlows(dataplaneTag uint8) error {
	cacheKey := fmt.Sprintf("%x", dataplaneTag)
	return c.deleteFlows(c.featureTraceflow.cachedFlows, cacheKey)
}

// InitialTLVMap adds TLV map optClass 0x0104, optType 0x80 optLength 4 tunMetadataIndex 0 to store data plane tag
// in tunnel. Data plane tag will be stored to NXM_NX_TUN_METADATA0[28..31] when packet get encapsulated
// into geneve, and will be stored back to NXM_NX_REG9[28..31] when packet get decapsulated.
func (c *client) InitialTLVMap() error {
	return c.bridge.AddTLVMap(0x0104, 0x80, 4, 0)
}

// setBasePacketOutBuilder sets base IP properties of a packetOutBuilder which can have more packet data added.
func setBasePacketOutBuilder(packetOutBuilder binding.PacketOutBuilder, srcMAC string, dstMAC string, srcIP string, dstIP string, inPort uint32, outPort uint32) (binding.PacketOutBuilder, error) {
	// Set ethernet header.
	parsedSrcMAC, err := net.ParseMAC(srcMAC)
	if err != nil {
		return nil, err
	}
	parsedDstMAC, err := net.ParseMAC(dstMAC)
	if err != nil {
		return nil, err
	}
	packetOutBuilder = packetOutBuilder.SetSrcMAC(parsedSrcMAC)
	packetOutBuilder = packetOutBuilder.SetDstMAC(parsedDstMAC)

	// Set IP header.
	parsedSrcIP := net.ParseIP(srcIP)
	parsedDstIP := net.ParseIP(dstIP)
	if parsedSrcIP == nil || parsedDstIP == nil {
		return nil, fmt.Errorf("invalid IP")
	}
	isIPv6 := parsedSrcIP.To4() == nil
	if isIPv6 != (parsedDstIP.To4() == nil) {
		return nil, fmt.Errorf("IP version mismatch")
	}
	packetOutBuilder = packetOutBuilder.SetSrcIP(parsedSrcIP)
	packetOutBuilder = packetOutBuilder.SetDstIP(parsedDstIP)

	packetOutBuilder = packetOutBuilder.SetTTL(128)

	packetOutBuilder = packetOutBuilder.SetInport(inPort)
	if outPort != 0 {
		packetOutBuilder = packetOutBuilder.SetOutport(outPort)
	}

	return packetOutBuilder, nil
}

// SendTCPReject generates TCP packet as a packet-out and sends it to OVS.
func (c *client) SendTCPPacketOut(
	srcMAC string,
	dstMAC string,
	srcIP string,
	dstIP string,
	inPort uint32,
	outPort uint32,
	isIPv6 bool,
	tcpSrcPort uint16,
	tcpDstPort uint16,
	tcpAckNum uint32,
	tcpFlag uint8,
	mutatePacketOut func(builder binding.PacketOutBuilder) binding.PacketOutBuilder) error {
	// Generate a base IP PacketOutBuilder.
	packetOutBuilder, err := setBasePacketOutBuilder(c.bridge.BuildPacketOut(), srcMAC, dstMAC, srcIP, dstIP, inPort, outPort)
	if err != nil {
		return err
	}
	// Set protocol.
	if isIPv6 {
		packetOutBuilder = packetOutBuilder.SetIPProtocol(binding.ProtocolTCPv6)
	} else {
		packetOutBuilder = packetOutBuilder.SetIPProtocol(binding.ProtocolTCP)
	}
	// Set TCP header data.
	packetOutBuilder = packetOutBuilder.SetTCPSrcPort(tcpSrcPort)
	packetOutBuilder = packetOutBuilder.SetTCPDstPort(tcpDstPort)
	packetOutBuilder = packetOutBuilder.SetTCPAckNum(tcpAckNum)
	packetOutBuilder = packetOutBuilder.SetTCPFlags(tcpFlag)

	if mutatePacketOut != nil {
		packetOutBuilder = mutatePacketOut(packetOutBuilder)
	}

	packetOutObj := packetOutBuilder.Done()
	return c.bridge.SendPacketOut(packetOutObj)
}

// SendICMPReject generates ICMP packet as a packet-out and send it to OVS.
func (c *client) SendICMPPacketOut(
	srcMAC string,
	dstMAC string,
	srcIP string,
	dstIP string,
	inPort uint32,
	outPort uint32,
	isIPv6 bool,
	icmpType uint8,
	icmpCode uint8,
	icmpData []byte,
	mutatePacketOut func(builder binding.PacketOutBuilder) binding.PacketOutBuilder) error {
	// Generate a base IP PacketOutBuilder.
	packetOutBuilder, err := setBasePacketOutBuilder(c.bridge.BuildPacketOut(), srcMAC, dstMAC, srcIP, dstIP, inPort, outPort)
	if err != nil {
		return err
	}
	// Set protocol.
	if isIPv6 {
		packetOutBuilder = packetOutBuilder.SetIPProtocol(binding.ProtocolICMPv6)
	} else {
		packetOutBuilder = packetOutBuilder.SetIPProtocol(binding.ProtocolICMP)
	}
	// Set ICMP header data.
	packetOutBuilder = packetOutBuilder.SetICMPType(icmpType)
	packetOutBuilder = packetOutBuilder.SetICMPCode(icmpCode)
	packetOutBuilder = packetOutBuilder.SetICMPData(icmpData)

	if mutatePacketOut != nil {
		packetOutBuilder = mutatePacketOut(packetOutBuilder)
	}

	packetOutObj := packetOutBuilder.Done()
	return c.bridge.SendPacketOut(packetOutObj)
}

// SendUDPPacketOut generates UDP packet as a packet-out and sends it to OVS.
func (c *client) SendUDPPacketOut(
	srcMAC string,
	dstMAC string,
	srcIP string,
	dstIP string,
	inPort uint32,
	outPort uint32,
	isIPv6 bool,
	udpSrcPort uint16,
	udpDstPort uint16,
	udpData []byte,
	mutatePacketOut func(builder binding.PacketOutBuilder) binding.PacketOutBuilder) error {
	// Generate a base IP PacketOutBuilder.
	packetOutBuilder, err := setBasePacketOutBuilder(c.bridge.BuildPacketOut(), srcMAC, dstMAC, srcIP, dstIP, inPort, outPort)
	if err != nil {
		return err
	}
	// Set protocol.
	if isIPv6 {
		packetOutBuilder = packetOutBuilder.SetIPProtocol(binding.ProtocolUDPv6)
	} else {
		packetOutBuilder = packetOutBuilder.SetIPProtocol(binding.ProtocolUDP)
	}
	// Set UDP header data.
	packetOutBuilder = packetOutBuilder.SetUDPSrcPort(udpSrcPort).
		SetUDPDstPort(udpDstPort).
		SetUDPData(udpData)

	if mutatePacketOut != nil {
		packetOutBuilder = mutatePacketOut(packetOutBuilder)
	}

	packetOutObj := packetOutBuilder.Done()
	return c.bridge.SendPacketOut(packetOutObj)
}

func (c *client) InstallMulticastInitialFlows(pktInReason uint8) error {
	flows := c.featureMulticast.igmpPktInFlows(pktInReason)
	flows = append(flows, c.featureMulticast.externalMulticastReceiverFlow())
	flows = append(flows, c.featureMulticast.multicastSkipIGMPMetricFlows()...)
	cacheKey := "multicast"
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()
	return c.addFlows(c.featureMulticast.cachedFlows, cacheKey, flows)
}

func (c *client) InstallMulticastFlows(multicastIP net.IP, groupID binding.GroupIDType) error {
	flows := c.featureMulticast.localMulticastForwardFlows(multicastIP, groupID)
	cacheKey := fmt.Sprintf("multicast_%s", multicastIP.String())
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()
	return c.addFlows(c.featureMulticast.cachedFlows, cacheKey, flows)
}

func (c *client) UninstallMulticastFlows(multicastIP net.IP) error {
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()
	cacheKey := fmt.Sprintf("multicast_%s", multicastIP.String())
	return c.deleteFlows(c.featureMulticast.cachedFlows, cacheKey)
}

func (c *client) InstallMulticastRemoteReportFlows(groupID binding.GroupIDType) error {
	firstMulticastTable := c.pipelines[pipelineMulticast].GetFirstTable()
	flows := c.featureMulticast.multicastRemoteReportFlows(groupID, firstMulticastTable)
	cacheKey := "multicast_encap"
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()
	return c.addFlows(c.featureMulticast.cachedFlows, cacheKey, flows)
}

func (c *client) SendIGMPQueryPacketOut(
	dstMAC net.HardwareAddr,
	dstIP net.IP,
	outPort uint32,
	igmp ofutil.Message) error {
	// Generate a base IP PacketOutBuilder.
	srcMAC := c.nodeConfig.GatewayConfig.MAC.String()
	srcIP := c.nodeConfig.GatewayConfig.IPv4.String()
	dstMACStr := dstMAC.String()
	dstIPStr := dstIP.String()
	packetOutBuilder, err := setBasePacketOutBuilder(c.bridge.BuildPacketOut(), srcMAC, dstMACStr, srcIP, dstIPStr, c.nodeConfig.GatewayConfig.OFPort, outPort)
	if err != nil {
		return err
	}
	// Set protocol and L4 message.
	packetOutBuilder = packetOutBuilder.SetIPProtocol(binding.ProtocolIGMP).SetL4Packet(igmp)
	packetOutObj := packetOutBuilder.Done()
	return c.bridge.SendPacketOut(packetOutObj)
}

func (c *client) InstallTrafficControlMarkFlows(name string, sourceOFPorts []uint32, targetOFPort uint32, direction v1alpha2.Direction, action v1alpha2.TrafficControlAction) error {
	flows := c.featurePodConnectivity.trafficControlMarkFlows(sourceOFPorts, targetOFPort, direction, action)
	cacheKey := fmt.Sprintf("tc_%s", name)
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()
	return c.modifyFlows(c.featurePodConnectivity.tcCachedFlows, cacheKey, flows)
}

func (c *client) UninstallTrafficControlMarkFlows(name string) error {
	cacheKey := fmt.Sprintf("tc_%s", name)
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()
	return c.deleteFlows(c.featurePodConnectivity.tcCachedFlows, cacheKey)
}

func (c *client) InstallTrafficControlReturnPortFlow(returnOFPort uint32) error {
	cacheKey := fmt.Sprintf("tc_%d", returnOFPort)
	flows := []binding.Flow{c.featurePodConnectivity.trafficControlReturnClassifierFlow(returnOFPort)}
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()
	return c.addFlows(c.featurePodConnectivity.tcCachedFlows, cacheKey, flows)
}

func (c *client) UninstallTrafficControlReturnPortFlow(returnOFPort uint32) error {
	cacheKey := fmt.Sprintf("tc_%d", returnOFPort)
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()
	return c.deleteFlows(c.featurePodConnectivity.tcCachedFlows, cacheKey)
}

func (c *client) SendIGMPRemoteReportPacketOut(
	dstMAC net.HardwareAddr,
	dstIP net.IP,
	igmp ofutil.Message) error {
	srcMAC := c.nodeConfig.GatewayConfig.MAC.String()
	srcIP := c.nodeConfig.NodeTransportIPv4Addr.IP.String()
	dstMACStr := dstMAC.String()
	dstIPStr := dstIP.String()
	packetOutBuilder, err := setBasePacketOutBuilder(c.bridge.BuildPacketOut(), srcMAC, dstMACStr, srcIP, dstIPStr, openflow15.P_CONTROLLER, 0)
	if err != nil {
		return err
	}
	// Set protocol, L4 message, and target OF Group ID.
	packetOutBuilder = packetOutBuilder.SetIPProtocol(binding.ProtocolIGMP).SetL4Packet(igmp)
	packetOutObj := packetOutBuilder.Done()
	return c.bridge.SendPacketOut(packetOutObj)
}

func (c *client) InstallMulticastGroup(groupID binding.GroupIDType, localReceivers []uint32, remoteNodeReceivers []net.IP) error {
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()
	table := MulticastOutputTable
	if c.enableAntreaPolicy {
		table = MulticastIngressRuleTable
	}

	if err := c.featureMulticast.multicastReceiversGroup(groupID, table.GetID(), localReceivers, remoteNodeReceivers); err != nil {
		return err
	}
	return nil
}

// InstallMulticlusterNodeFlows installs flows to handle cross-cluster packets between a regular
// Node and a local Gateway.
func (c *client) InstallMulticlusterNodeFlows(clusterID string,
	peerConfigs map[*net.IPNet]net.IP,
	tunnelPeerIP net.IP) error {
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()
	cacheKey := fmt.Sprintf("cluster_%s", clusterID)
	var flows []binding.Flow
	localGatewayMAC := c.nodeConfig.GatewayConfig.MAC
	for peerCIDR, remoteGatewayIP := range peerConfigs {
		flows = append(flows, c.featureMulticluster.l3FwdFlowToRemoteViaTun(localGatewayMAC, *peerCIDR, tunnelPeerIP, remoteGatewayIP)...)
	}
	return c.modifyFlows(c.featureMulticluster.cachedFlows, cacheKey, flows)
}

// InstallMulticlusterGatewayFlows installs flows to handle cross-cluster packets between Gateways.
func (c *client) InstallMulticlusterGatewayFlows(clusterID string,
	peerConfigs map[*net.IPNet]net.IP,
	tunnelPeerIP net.IP,
	localGatewayIP net.IP,
) error {
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()
	cacheKey := fmt.Sprintf("cluster_%s", clusterID)
	var flows []binding.Flow
	localGatewayMAC := c.nodeConfig.GatewayConfig.MAC
	for peerCIDR, remoteGatewayIP := range peerConfigs {
		flows = append(flows, c.featureMulticluster.l3FwdFlowToRemoteViaTun(localGatewayMAC, *peerCIDR, tunnelPeerIP, remoteGatewayIP)...)
		// Add SNAT flows to change cross-cluster packets' source IP to local Gateway IP.
		flows = append(flows, c.featureMulticluster.snatConntrackFlows(*peerCIDR, localGatewayIP)...)
	}
	return c.modifyFlows(c.featureMulticluster.cachedFlows, cacheKey, flows)
}

// InstallMulticlusterClassifierFlows adds the following flows:
//   - One flow in L2ForwardingCalcTable for the global virtual multicluster MAC 'aa:bb:cc:dd:ee:f0'
//     to set its target output port as 'antrea-tun0'. This flow will be on both Gateway and regular Node.
//   - One flow to match MC virtual MAC 'aa:bb:cc:dd:ee:f0' in ClassifierTable for Gateway only.
//   - One flow in L2ForwardingOutTable to allow multicluster hairpin traffic for Gateway only.
func (c *client) InstallMulticlusterClassifierFlows(tunnelOFPort uint32, isGateway bool) error {
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()

	flows := []binding.Flow{
		c.featurePodConnectivity.l2ForwardCalcFlow(GlobalVirtualMACForMulticluster, tunnelOFPort),
	}

	if isGateway {
		flows = append(flows,
			c.featureMulticluster.tunnelClassifierFlow(tunnelOFPort),
			c.featureMulticluster.outputHairpinTunnelFlow(tunnelOFPort),
		)
	}
	return c.modifyFlows(c.featureMulticluster.cachedFlows, "multicluster-classifier", flows)
}

func (c *client) UninstallMulticlusterFlows(clusterID string) error {
	c.replayMutex.RLock()
	defer c.replayMutex.RUnlock()
	cacheKey := fmt.Sprintf("cluster_%s", clusterID)
	return c.deleteFlows(c.featureMulticluster.cachedFlows, cacheKey)
}
