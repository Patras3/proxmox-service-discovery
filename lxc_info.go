package main

import (
	"github.com/andrew-d/proxmox-service-discovery/internal/lazy"
	"github.com/andrew-d/proxmox-service-discovery/internal/pveapi"
)

// lxcInformation represents information about a single LXC container,
// lazily calculating derived information as needed.
type lxcInformation struct {
	ID         int                   // Container ID
	Node       string                // Node that the container is located on
	Config     pveapi.LXCConfig      // LXC configuration for the container
	Interfaces []pveapi.LXCInterface // Network interfaces for the container

	lazyNetworkKVs     lazy.Map[int, map[string]string] // map[ifnum]KVs
	lazyBridgeByHwaddr lazy.Value[map[string]string]    // { "hwaddr": "vmbr0" }
}

// getNetworkKV retrieves the split key-value pairs for a given network
// interface number.
//
// For example, if the LXC config has:
//
//	net0: bridge=vmbr0
//
// Then getNetworkKV(0) will return:
//
//	map[string]string{
//	    "bridge": "vmbr0",
//	}
func (l *lxcInformation) getNetworkKV(ifnum int) map[string]string {
	return l.lazyNetworkKVs.Get(ifnum, func(int) map[string]string {
		if conf, ok := l.Config.NetworkInterfaces[ifnum]; ok {
			return splitKVs(conf)
		}
		return nil
	})
}
