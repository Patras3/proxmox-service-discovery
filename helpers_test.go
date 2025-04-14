package main

import (
	"net/netip"

	"github.com/andrew-d/proxmox-service-discovery/internal/pveapi"
)

// This function contains a bunch of helper functions for constructing test
// data. They panic if misused.

// mkNet0 is a helper function to return a map[string]string with a key of
// "net0" and a value of the provided string; useful for the NetworkInterfaces
// field of QEMUConfig.
func mkNet0(s string) map[int]string {
	return map[int]string{0: s}
}

// mkAgentInterfaceAddrs is a helper function to return a slice of
// [pveapi.AgentInterfaceAddress] from the provided slice of strings.
//
// Each string is a CIDR that will be parsed with [netip.ParsePrefix].
func mkAgentInterfaceAddrs(addrs ...string) []pveapi.AgentInterfaceAddress {
	var result []pveapi.AgentInterfaceAddress
	for _, addr := range addrs {
		pfx := netip.MustParsePrefix(addr)

		var ty string
		if pfx.Addr().Is4() {
			ty = "ipv4"
		} else {
			ty = "ipv6"
		}
		result = append(result, pveapi.AgentInterfaceAddress{
			Type:    ty,
			Address: pfx.Addr().String(),
			Prefix:  pfx.Bits(),
		})
	}
	return result
}

// mkPVEInventoryAddr is a helper function to return a [pveInventoryAddr] from
// the given bridge and one or more addresses.
func mkPVEInventoryAddr(bridge string, addrs ...string) pveInventoryAddr {
	var naddrs []netip.Addr
	for _, addr := range addrs {
		naddrs = append(naddrs, netip.MustParseAddr(addr))
	}

	return pveInventoryAddr{
		Bridge: bridge,
		Addrs:  naddrs,
	}
}
