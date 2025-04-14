package main

import (
	"strings"

	"github.com/andrew-d/proxmox-service-discovery/internal/lazy"
	"github.com/andrew-d/proxmox-service-discovery/internal/pveapi"
)

// qemuInformation represents information about a single QEMU virtual machine,
// lazily calculating derived information as needed.
type qemuInformation struct {
	ID     int               // VM ID
	Node   string            // Node that the VM is located on
	Config pveapi.QEMUConfig // QEMU configuration for the VM

	lazyNetworkKVs     lazy.Map[int, map[string]string] // map[ifnum]KVs
	lazyIPConfigs      lazy.Map[int, map[string]string] // map[ipConfigNum]KVs
	lazyBridgeByIfnum  lazy.Map[int, string]            // { 0: "vmbr0", 1: "vmbr1", etc. }
	lazyAllHwaddrs     lazy.Value[map[string]bool]      // { "hwaddr": true }
	lazyBridgeByHwaddr lazy.Value[map[string]string]    // { "hwaddr": "vmbr0" }
}

// getNetworkKV retrieves the split key-value pairs for a given network
// interface number.
//
// For example, if the QEMU config has:
//
//	net0: virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0
//
// Then getNetworkKV(0) will return:
//
//	map[string]string{
//	    "virtio": "AA:BB:CC:DD:EE:FF",
//	    "bridge": "vmbr0",
//	}
func (q *qemuInformation) getNetworkKV(ifnum int) map[string]string {
	return q.lazyNetworkKVs.Get(ifnum, func(int) map[string]string {
		if conf, ok := q.Config.NetworkInterfaces[ifnum]; ok {
			return splitKVs(conf)
		}
		return nil
	})
}

// getIPConfig returns the IP configuration (for cloud-init) for a given
// configuration number.
//
// For example, if the QEMU config has:
//
//	ipconfig0: ip=192.168.4.33/24,gw=192.168.4.1
//
// Then getIPConfig(0) will return:
//
//	map[string]string{
//	    "ip": "192.168.4.33/24",
//	    "gw": "192.168.4.1",
//	}
func (q *qemuInformation) getIPConfig(ifnum int) map[string]string {
	return q.lazyIPConfigs.Get(ifnum, func(int) map[string]string {
		if conf, ok := q.Config.IPConfig[ifnum]; ok {
			return splitKVs(conf)
		}
		return nil
	})
}

// getBridgeByIfnum will return the bridge name for the given network interface
// number, or the empty string if the interface number does not exist or does
// not have a bridge.
func (q *qemuInformation) getBridgeByIfnum(ifnum int) string {
	return q.lazyBridgeByIfnum.Get(ifnum, func(int) string {
		conf := q.getNetworkKV(ifnum)
		if conf == nil {
			return ""
		}
		return conf["bridge"]
	})
}

// getAllHwaddrs returns a map of all configured hardware addresses (a.k.a. MAC
// addresses) for the VM. The keys are the hardware addresses, and the values
// are always true.
func (q *qemuInformation) getAllHwaddrs() map[string]bool {
	return q.lazyAllHwaddrs.Get(func() map[string]bool {
		allHwaddrs := make(map[string]bool)
		for ifnum := range q.Config.NetworkInterfaces {
			kvs := q.getNetworkKV(ifnum)
			if kvs == nil {
				continue
			}
			if addr := hwaddrForQEMUInterface(kvs); addr != "" {
				allHwaddrs[addr] = true
			}
		}
		return allHwaddrs
	})
}

// getBridgeByHwaddr will return the bridge name for the given hardware address, or
// the empty string if the hardware address does not exist or does not have a
// configured bridge.
//
// For example, if the QEMU config has:
//
//	net0: virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0
//
// Then getBridgeByHwaddr("AA:BB:CC:DD:EE:FF") will return "vmbr0".
func (q *qemuInformation) getBridgeByHwaddr(hwaddr string) string {
	// Lazily calculate the bridgeByHwaddr map.
	hwaddrs := q.lazyBridgeByHwaddr.Get(func() map[string]string {
		bridgeByHwaddr := make(map[string]string)
		for ifnum := range q.Config.NetworkInterfaces {
			kvs := q.getNetworkKV(ifnum)
			if kvs == nil {
				continue
			}
			addr := hwaddrForQEMUInterface(kvs)
			if addr == "" {
				continue // no hwaddr
			}

			if bridge := kvs["bridge"]; bridge != "" {
				bridgeByHwaddr[addr] = bridge
			}
		}
		return bridgeByHwaddr
	})
	return hwaddrs[strings.ToLower(hwaddr)]
}
